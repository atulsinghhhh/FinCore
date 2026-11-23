package idempotency

import (
	"context"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"fincore/internal/testutil"
)

var rdb *redis.Client

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	client, cleanup, err := testutil.StartDragonfly(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	rdb = client
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// newServer wraps handler with the middleware and counts how often it runs.
func newServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	if rdb == nil {
		t.Skip("integration test: run without -short")
	}
	var calls atomic.Int32
	store := NewStore(rdb)
	store.Prefix = "test:" + uuid.NewString() + ":"
	h := store.Middleware(func(*http.Request) string { return "" })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, &calls
}

func post(t *testing.T, url, key, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	if key != "" {
		req.Header.Set(Header, key)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(b)
}

func created(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", "/v1/things/1")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"id":"` + uuid.NewString() + `"}`))
}

func TestReplaysStoredResponse(t *testing.T) {
	srv, calls := newServer(t, created)
	key := uuid.NewString()

	first, firstBody := post(t, srv.URL+"/things", key, `{"amount":100}`)
	require.Equal(t, http.StatusCreated, first.StatusCode)
	require.Empty(t, first.Header.Get(ReplayedHeader))

	for range 5 {
		again, body := post(t, srv.URL+"/things", key, `{"amount":100}`)
		require.Equal(t, http.StatusCreated, again.StatusCode)
		require.Equal(t, firstBody, body, "byte-for-byte replay")
		require.Equal(t, "true", again.Header.Get(ReplayedHeader))
		require.Equal(t, "/v1/things/1", again.Header.Get("Location"))
	}
	require.EqualValues(t, 1, calls.Load(), "handler ran once")
}

func TestKeyReusedWithDifferentRequest(t *testing.T) {
	srv, calls := newServer(t, created)
	key := uuid.NewString()
	post(t, srv.URL+"/things", key, `{"amount":100}`)

	resp, body := post(t, srv.URL+"/things", key, `{"amount":999}`)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Contains(t, body, "idempotency_key_reused")

	resp, _ = post(t, srv.URL+"/other", key, `{"amount":100}`)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "same key, different endpoint")
	require.EqualValues(t, 1, calls.Load())
}

func TestRequiresKey(t *testing.T) {
	srv, calls := newServer(t, created)
	resp, body := post(t, srv.URL+"/things", "", `{}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, body, "idempotency_key_required")
	resp, _ = post(t, srv.URL+"/things", strings.Repeat("k", 256), `{}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Zero(t, calls.Load())

	get, err := http.Get(srv.URL + "/things")
	require.NoError(t, err)
	get.Body.Close()
	require.Equal(t, http.StatusCreated, get.StatusCode, "GET is not intercepted")
}

func TestConcurrentDuplicateGetsConflict(t *testing.T) {
	release := make(chan struct{})
	srv, calls := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		created(w, r)
	})
	key := uuid.NewString()

	var wg sync.WaitGroup
	wg.Go(func() {
		resp, _ := post(t, srv.URL+"/things", key, `{}`)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	})
	require.Eventually(t, func() bool { return calls.Load() == 1 }, 5*time.Second, 10*time.Millisecond)

	resp, body := post(t, srv.URL+"/things", key, `{}`)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.Contains(t, body, "request_in_progress")
	require.Equal(t, "1", resp.Header.Get("Retry-After"))

	close(release)
	wg.Wait()
	resp, _ = post(t, srv.URL+"/things", key, `{}`)
	require.Equal(t, "true", resp.Header.Get(ReplayedHeader))
	require.EqualValues(t, 1, calls.Load())
}

func TestServerErrorsAreNotCached(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv, calls := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		created(w, r)
	})
	key := uuid.NewString()

	resp, _ := post(t, srv.URL+"/things", key, `{}`)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	fail.Store(false)
	resp, _ = post(t, srv.URL+"/things", key, `{}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "retry after a 5xx runs again")
	require.Empty(t, resp.Header.Get(ReplayedHeader))
	require.EqualValues(t, 2, calls.Load())
}

func TestClientErrorsAreCached(t *testing.T) {
	srv, calls := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"insufficient_funds"}`, http.StatusUnprocessableEntity)
	})
	key := uuid.NewString()
	post(t, srv.URL+"/things", key, `{}`)
	resp, _ := post(t, srv.URL+"/things", key, `{}`)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Equal(t, "true", resp.Header.Get(ReplayedHeader))
	require.EqualValues(t, 1, calls.Load())
}

func TestPanicReleasesKey(t *testing.T) {
	var panicked atomic.Bool
	srv, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !panicked.Swap(true) {
			panic("handler bug")
		}
		created(w, r)
	})
	key := uuid.NewString()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/things", strings.NewReader(`{}`))
	req.Header.Set(Header, key)
	if resp, err := http.DefaultClient.Do(req); err == nil { // net/http drops the connection on panic
		resp.Body.Close()
	}

	resp, _ := post(t, srv.URL+"/things", key, `{}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "a crashed request must not block retries")
}

func TestFailsOpenWhenStoreIsDown(t *testing.T) {
	down := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	defer down.Close()
	var calls atomic.Int32
	h := NewStore(down).Middleware(func(*http.Request) string { return "" })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		created(w, r)
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, _ := post(t, srv.URL+"/things", "k", `{}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.EqualValues(t, 1, calls.Load())
}

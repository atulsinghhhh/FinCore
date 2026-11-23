package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"fincore/internal/testutil"
)

func TestFixedWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	rdb, cleanup, err := testutil.StartDragonfly(context.Background())
	require.NoError(t, err)
	defer cleanup()

	l := New(rdb, 3, time.Second)
	l.Prefix = "rl-test:" + uuid.NewString() + ":"
	h := l.Middleware(func(r *http.Request) string { return r.Header.Get("X-Client") })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	call := func(client string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Client", client)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for i := range 3 {
		rec := call("a")
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, string(rune('2'-i)), rec.Header().Get("X-RateLimit-Remaining"))
	}
	limited := call("a")
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	require.NotEmpty(t, limited.Header().Get("Retry-After"))
	require.Equal(t, http.StatusOK, call("b").Code, "limits are per client")

	time.Sleep(1100 * time.Millisecond)
	require.Equal(t, http.StatusOK, call("a").Code, "a new window starts")
}

func TestFailsOpen(t *testing.T) {
	down := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 50 * time.Millisecond})
	defer down.Close()
	h := New(down, 1, time.Second).Middleware(func(*http.Request) string { return "x" })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for range 3 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}
}

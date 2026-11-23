package outbox

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"fincore/internal/ledger"
	"fincore/internal/payments"
	"fincore/internal/provider"
	"fincore/internal/testutil"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	pool, cleanup, err := testutil.StartPostgres(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

type receiver struct {
	mu       sync.Mutex
	secret   string
	events   []Event
	failNext atomic.Int32
	server   *httptest.Server
	t        *testing.T
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{t: t}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.failNext.Load() > 0 {
			r.failNext.Add(-1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := Verify(r.secret, req.Header.Get(SignatureHeader), body, 5*time.Minute, time.Now()); err != nil {
			t.Errorf("signature: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var ev Event
		require.NoError(t, json.Unmarshal(body, &ev))
		require.Equal(t, ev.ID, req.Header.Get(EventIDHeader))
		r.events = append(r.events, ev)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *receiver) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		out = append(out, e.Type)
	}
	return out
}

func setup(t *testing.T) (*payments.Service, *Dispatcher, payments.Merchant) {
	t.Helper()
	if testPool == nil {
		t.Skip("integration test: run without -short")
	}
	svc := payments.NewService(testPool, ledger.New(testPool), provider.NewSandbox())
	m, err := svc.CreateMerchant(context.Background(), payments.CreateMerchantRequest{Name: "M", Currency: "INR", FeeBps: 200})
	require.NoError(t, err)
	d := NewDispatcher(testPool)
	d.Backoff = func(int) time.Duration { return 0 } // retry immediately in tests
	d.BlockedDelay = 0
	return svc, d, m
}

func pay(t *testing.T, svc *payments.Service, m payments.Merchant, amount int64) payments.Payment {
	p, err := svc.CreatePayment(context.Background(), payments.CreatePaymentRequest{
		MerchantID: m.ID, IdempotencyKey: uuid.NewString(), Amount: amount, Currency: "INR", PaymentMethod: provider.PMCardSuccess,
	})
	require.NoError(t, err)
	return p
}

func drain(t *testing.T, d *Dispatcher) {
	t.Helper()
	for {
		n, err := d.DispatchOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			return
		}
	}
}

func merchantStatuses(t *testing.T, merchantID uuid.UUID) map[string]int {
	rows, err := testPool.Query(context.Background(), `SELECT status, count(*) FROM outbox_events WHERE merchant_id = $1 GROUP BY status`, merchantID)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		require.NoError(t, rows.Scan(&s, &n))
		out[s] = n
	}
	return out
}

func TestDeliversSignedEventsInOrder(t *testing.T) {
	svc, d, m := setup(t)
	recv := newReceiver(t)
	ep, err := svc.SetMerchantWebhook(context.Background(), m.ID, recv.server.URL)
	require.NoError(t, err)
	recv.secret = ep.Secret

	p := pay(t, svc, m, 10000)
	_, err = svc.Refund(context.Background(), p.ID, payments.RefundRequest{IdempotencyKey: uuid.NewString(), Amount: ptr(int64(1000))})
	require.NoError(t, err)
	drain(t, d)

	require.Equal(t, []string{
		"payment.created", "payment.authorized", "payment.captured", "refund.created", "payment.partially_refunded",
	}, recv.types())
	var data payments.Payment
	require.NoError(t, json.Unmarshal(recv.events[2].Data, &data))
	require.Equal(t, p.ID, data.ID)
	require.Equal(t, map[string]int{StatusDelivered: 5}, merchantStatuses(t, m.ID))
}

func TestNoEndpointMeansSkipped(t *testing.T) {
	svc, d, m := setup(t)
	pay(t, svc, m, 500)
	drain(t, d)
	require.Equal(t, map[string]int{StatusSkipped: 3}, merchantStatuses(t, m.ID))
}

func TestRetriesThenDeadLetters(t *testing.T) {
	svc, d, m := setup(t)
	recv := newReceiver(t)
	ep, err := svc.SetMerchantWebhook(context.Background(), m.ID, recv.server.URL)
	require.NoError(t, err)
	recv.secret = ep.Secret

	_, err = svc.CreatePayout(context.Background(), payments.CreatePayoutRequest{MerchantID: m.ID, IdempotencyKey: uuid.NewString(), Amount: 1})
	require.ErrorIs(t, err, payments.ErrInsufficientFunds) // no events from a failed operation
	pay(t, svc, m, 500)

	recv.failNext.Store(2) // first two deliveries fail, then succeed
	drain(t, d)
	require.Len(t, recv.types(), 3)
	require.Equal(t, map[string]int{StatusDelivered: 3}, merchantStatuses(t, m.ID))

	d.MaxAttempts = 2
	recv.failNext.Store(1000)
	pay(t, svc, m, 500)
	drain(t, d)
	require.Equal(t, map[string]int{StatusDelivered: 3, StatusDead: 3}, merchantStatuses(t, m.ID))

	var lastErr string
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT last_error FROM outbox_events WHERE merchant_id = $1 AND status = 'dead' LIMIT 1`, m.ID).Scan(&lastErr))
	require.Contains(t, lastErr, "503")
}

func TestConcurrentDispatchersDeliverOnce(t *testing.T) {
	svc, d, m := setup(t)
	var hits atomic.Int32
	seen := sync.Map{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if _, dup := seen.LoadOrStore(r.Header.Get(EventIDHeader), true); dup {
			t.Errorf("event %s delivered twice", r.Header.Get(EventIDHeader))
		}
	}))
	defer srv.Close()
	_, err := svc.SetMerchantWebhook(context.Background(), m.ID, srv.URL)
	require.NoError(t, err)
	for range 20 {
		pay(t, svc, m, 100)
	}

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() { drain(t, d) })
	}
	wg.Wait()
	require.EqualValues(t, 60, hits.Load())
}

func TestVerify(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	now := time.Now()
	header := Sign("whsec_x", now, body)
	require.NoError(t, Verify("whsec_x", header, body, time.Minute, now))
	require.ErrorIs(t, Verify("whsec_y", header, body, time.Minute, now), ErrInvalidSignature)
	require.ErrorIs(t, Verify("whsec_x", header, []byte(`{"id":"evt_2"}`), time.Minute, now), ErrInvalidSignature)
	require.ErrorIs(t, Verify("whsec_x", header, body, time.Minute, now.Add(10*time.Minute)), ErrInvalidSignature, "replayed too late")
	require.ErrorIs(t, Verify("whsec_x", "garbage", body, time.Minute, now), ErrInvalidSignature)
}

func ptr[T any](v T) *T { return &v }

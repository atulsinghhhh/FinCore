package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"fincore/internal/idempotency"
	"fincore/internal/ledger"
	"fincore/internal/operators"
	"fincore/internal/payments"
	"fincore/internal/provider"
	"fincore/internal/provider/razorpay"
	"fincore/internal/testutil"
)

const adminToken = "test-admin-token-0123456789abcdef0123456789"

var (
	pool *pgxpool.Pool
	rdb  *redis.Client
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	var cleanPG, cleanDF func()
	var err error
	if pool, cleanPG, err = testutil.StartPostgres(ctx); err != nil {
		log.Fatal(err)
	}
	if rdb, cleanDF, err = testutil.StartDragonfly(ctx); err != nil {
		cleanPG()
		log.Fatal(err)
	}
	code := m.Run()
	cleanDF()
	cleanPG()
	os.Exit(code)
}

type client struct {
	t     *testing.T
	url   string
	token string
}

func newServer(t *testing.T, rzp *razorpay.Client, providers ...provider.Provider) *Server {
	t.Helper()
	if pool == nil {
		t.Skip("integration test: run without -short")
	}
	l := ledger.New(pool)
	srv := NewServer(payments.NewService(pool, l, providers...), l, idempotency.NewStore(rdb), map[string]HealthCheck{"postgres": pool.Ping})
	srv.Sandbox, srv.AdminToken, srv.Razorpay, srv.Redis = true, adminToken, rzp, rdb
	srv.Operators = operators.NewService(pool)
	return srv
}

// newAPI starts a sandbox-provider server and returns an admin client.
func newAPI(t *testing.T) client {
	return serve(t, newServer(t, nil, provider.NewSandbox()))
}

func serve(t *testing.T, srv *Server) client {
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return client{t: t, url: ts.URL, token: adminToken}
}

func (c client) as(token string) client { c.token = token; return c }

// merchant creates a merchant via the admin API and returns a client
// authenticated with its API key.
func (c client) merchant(name string) (client, string) {
	c.t.Helper()
	r := c.post("/v1/admin/merchants", map[string]any{"name": name, "currency": "INR", "fee_bps": 200})
	require.Equal(c.t, http.StatusCreated, r.status, r.body)
	key := r.body["api_key"].(map[string]any)["secret"].(string)
	require.True(c.t, strings.HasPrefix(key, "fc_test_"))
	return c.as(key), r.body["merchant"].(map[string]any)["id"].(string)
}

type response struct {
	status   int
	header   http.Header
	body     map[string]any
	replayed bool
}

func (c client) raw(method, path, key, contentType string, body io.Reader) response {
	c.t.Helper()
	req, err := http.NewRequest(method, c.url+path, body)
	require.NoError(c.t, err)
	if key != "" {
		req.Header.Set(idempotency.Header, key)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	out := response{status: resp.StatusCode, header: resp.Header, replayed: resp.Header.Get(idempotency.ReplayedHeader) == "true"}
	if len(data) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		require.NoError(c.t, json.Unmarshal(data, &out.body), string(data))
	}
	return out
}

func (c client) do(method, path, key string, body any) response {
	c.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(c.t, json.NewEncoder(&buf).Encode(body))
	}
	return c.raw(method, path, key, "application/json", &buf)
}

func (c client) post(path string, body any) response {
	return c.do(http.MethodPost, path, uuid.NewString(), body)
}
func (c client) get(path string) response { return c.do(http.MethodGet, path, "", nil) }

func errCode(r response) string {
	e, _ := r.body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func num(v any) int64 { return int64(v.(float64)) }

func TestEndToEnd(t *testing.T) {
	admin := newAPI(t)
	m, merchantID := admin.merchant("Chai Point")

	me := m.get("/v1/merchants/me")
	require.Equal(t, merchantID, me.body["id"])

	// Pay ₹1,000, automatically captured.
	payKey := uuid.NewString()
	payBody := map[string]any{"amount": 100000, "currency": "INR", "payment_method": provider.PMCardSuccess}
	pay := m.do(http.MethodPost, "/v1/payments", payKey, payBody)
	require.Equal(t, http.StatusCreated, pay.status, pay.body)
	require.Equal(t, "captured", pay.body["status"])
	require.EqualValues(t, 2000, num(pay.body["fee_amount"]))
	paymentID := pay.body["id"].(string)
	require.Equal(t, "/v1/payments/"+paymentID, pay.header.Get("Location"))

	// A network retry replays the exact response.
	again := m.do(http.MethodPost, "/v1/payments", payKey, payBody)
	require.True(t, again.replayed)
	require.Equal(t, pay.body, again.body)

	require.EqualValues(t, 98000, num(m.get("/v1/merchants/me/balance").body["available"]))

	// Partial refund, then payout of the rest, confirmed by the (simulated) bank.
	refund := m.post("/v1/payments/"+paymentID+"/refunds", map[string]any{"amount": 10000, "reason": "late delivery"})
	require.Equal(t, http.StatusCreated, refund.status, refund.body)
	require.Equal(t, "partially_refunded", m.get("/v1/payments/" + paymentID).body["status"])
	available := num(m.get("/v1/merchants/me/balance").body["available"])
	require.EqualValues(t, 98000-9800, available)

	payout := m.post("/v1/payouts", map[string]any{"amount": available})
	require.Equal(t, http.StatusCreated, payout.status, payout.body)
	tooMuch := m.post("/v1/payouts", map[string]any{"amount": 1})
	require.Equal(t, http.StatusUnprocessableEntity, tooMuch.status)
	require.Equal(t, "insufficient_funds", errCode(tooMuch))
	paid := admin.post("/v1/admin/sandbox/payouts/"+payout.body["id"].(string)+"/paid", nil)
	require.Equal(t, "paid", paid.body["status"], paid.body)

	require.Len(t, m.get("/v1/payments/" + paymentID + "/events").body["data"], 4)
	require.Len(t, m.get("/v1/payments").body["data"], 1)
	require.Len(t, m.get("/v1/payouts").body["data"], 1)

	st := m.get("/v1/merchants/me/statement")
	require.Equal(t, http.StatusOK, st.status, st.body)
	require.Len(t, st.body["lines"], 3)
	require.EqualValues(t, 0, num(st.body["closing_balance"]))

	detail := admin.get("/v1/admin/payments/" + paymentID)
	require.Len(t, detail.body["events"], 4)
	require.Len(t, detail.body["refunds"], 1)
	txn := admin.get("/v1/admin/ledger/transactions/" + detail.body["capture_ledger_txn_id"].(string))
	require.Len(t, txn.body["postings"], 3)

	integrity := admin.get("/v1/admin/ledger/integrity")
	require.Equal(t, true, integrity.body["ok"])
	stats := admin.get("/v1/admin/stats")
	require.GreaterOrEqual(t, num(stats.body["payments"]), int64(1))
}

func TestAuthentication(t *testing.T) {
	admin := newAPI(t)
	alice, _ := admin.merchant("Alice")
	bob, bobID := admin.merchant("Bob")

	require.Equal(t, http.StatusUnauthorized, alice.as("").get("/v1/payments").status)
	require.Equal(t, http.StatusUnauthorized, alice.as("fc_test_"+strings.Repeat("0", 64)).get("/v1/payments").status)
	require.Equal(t, http.StatusUnauthorized, alice.as("sk_live_whatever").get("/v1/payments").status)
	require.Equal(t, http.StatusUnauthorized, alice.get("/v1/admin/stats").status, "merchant keys are not admin")
	require.Equal(t, http.StatusUnauthorized, admin.as("wrong-admin-token").get("/v1/admin/stats").status)

	// Merchants cannot see or touch each other's payments.
	p := bob.post("/v1/payments", map[string]any{"amount": 5000, "currency": "INR", "payment_method": provider.PMCardSuccess})
	bobPayment := "/v1/payments/" + p.body["id"].(string)
	require.Equal(t, http.StatusNotFound, alice.get(bobPayment).status)
	require.Equal(t, http.StatusNotFound, alice.post(bobPayment+"/refunds", map[string]any{}).status)
	require.Equal(t, http.StatusOK, bob.get(bobPayment).status)

	// The body cannot pick a merchant: it comes from the key.
	spoof := alice.post("/v1/payments", map[string]any{"merchant_id": bobID, "amount": 1, "currency": "INR"})
	require.Equal(t, http.StatusBadRequest, spoof.status)

	// Idempotency keys are scoped per merchant.
	key := uuid.NewString()
	body := map[string]any{"amount": 700, "currency": "INR", "payment_method": provider.PMCardSuccess}
	a := alice.do(http.MethodPost, "/v1/payments", key, body)
	b := bob.do(http.MethodPost, "/v1/payments", key, body)
	require.Equal(t, http.StatusCreated, a.status)
	require.Equal(t, http.StatusCreated, b.status)
	require.False(t, b.replayed)
	require.NotEqual(t, a.body["id"], b.body["id"])

	// A live key is refused by a test server with a clear error.
	wrong := alice.as("fc_live_" + strings.Repeat("a", 64)).get("/v1/payments")
	require.Equal(t, http.StatusUnauthorized, wrong.status)
	require.Equal(t, "wrong_mode", errCode(wrong))
	require.Equal(t, "test", wrong.header.Get("FinCore-Mode"))

	// Revoked keys stop working immediately; new keys work.
	keys := admin.get("/v1/admin/merchants/" + bobID + "/api-keys").body["data"].([]any)
	keyID := keys[0].(map[string]any)["id"].(string)
	require.Nil(t, keys[0].(map[string]any)["secret"], "secrets are never listed")
	fresh := admin.post("/v1/admin/merchants/"+bobID+"/api-keys", nil)
	require.Equal(t, http.StatusCreated, fresh.status)
	revoked := admin.post("/v1/admin/merchants/"+bobID+"/api-keys/"+keyID+"/revoke", nil)
	require.Equal(t, http.StatusOK, revoked.status, revoked.body)
	require.Equal(t, http.StatusUnauthorized, bob.get("/v1/payments").status)
	require.Equal(t, http.StatusOK, bob.as(fresh.body["secret"].(string)).get("/v1/payments").status)

	// Both key actions land in the operator audit trail, named by merchant.
	events := admin.get("/v1/admin/operators/events").body["data"].([]any)
	var seen []string
	for _, e := range events {
		ev := e.(map[string]any)
		if ev["target"] == "Bob" {
			seen = append(seen, ev["action"].(string))
		}
	}
	require.Contains(t, seen, "api_key.issued")
	require.Contains(t, seen, "api_key.revoked")
}

func TestPagination(t *testing.T) {
	admin := newAPI(t)
	m, _ := admin.merchant("Pager")
	var created []string
	for i := range 5 {
		r := m.post("/v1/payments", map[string]any{"amount": 100 + i, "currency": "INR", "payment_method": provider.PMCardSuccess})
		created = append([]string{r.body["id"].(string)}, created...) // newest first
	}
	var seen []string
	cursor := ""
	pages := 0
	for {
		r := m.get("/v1/payments?limit=2" + cursor)
		require.Equal(t, http.StatusOK, r.status, r.body)
		pages++
		for _, p := range r.body["data"].([]any) {
			seen = append(seen, p.(map[string]any)["id"].(string))
		}
		if r.body["has_more"] != true {
			require.Nil(t, r.body["next_cursor"])
			break
		}
		cursor = "&cursor=" + r.body["next_cursor"].(string)
	}
	require.Equal(t, 3, pages)
	require.Equal(t, created, seen, "every payment exactly once, newest first")
	require.Equal(t, http.StatusBadRequest, m.get("/v1/payments?cursor=garbage").status)
}

func TestOperatorInviteFlowOverHTTP(t *testing.T) {
	admin := newAPI(t)
	addr := "http-" + uuid.NewString()[:8] + "@example.com"

	inv := admin.raw(http.MethodPost, "/v1/admin/invites", "", "application/json", strings.NewReader(`{"email":"`+addr+`","role":"viewer"}`))
	require.Equal(t, http.StatusCreated, inv.status, inv.body)
	token := inv.body["token"].(string)

	look := admin.raw(http.MethodPost, "/v1/admin/invites/lookup", "", "application/json", strings.NewReader(`{"token":"`+token+`"}`))
	require.Equal(t, addr, look.body["email"])

	acc := admin.raw(http.MethodPost, "/v1/admin/invites/accept", "", "application/json", strings.NewReader(`{"token":"`+token+`","name":"Http Viewer","password":"a long enough password"}`))
	require.Equal(t, http.StatusCreated, acc.status, acc.body)
	require.Nil(t, acc.body["password_hash"], "hashes never leave the API")

	again := admin.raw(http.MethodPost, "/v1/admin/invites/accept", "", "application/json", strings.NewReader(`{"token":"`+token+`","name":"X","password":"a long enough password"}`))
	require.Equal(t, http.StatusGone, again.status)

	ok := admin.raw(http.MethodPost, "/v1/admin/operators/login", "", "application/json", strings.NewReader(`{"email":"`+addr+`","password":"a long enough password"}`))
	require.Equal(t, http.StatusOK, ok.status)
	require.Equal(t, "viewer", ok.body["role"])
	bad := admin.raw(http.MethodPost, "/v1/admin/operators/login", "", "application/json", strings.NewReader(`{"email":"`+addr+`","password":"nope"}`))
	require.Equal(t, http.StatusUnauthorized, bad.status)

	m, _ := admin.merchant("NotAnOperator")
	require.Equal(t, http.StatusUnauthorized, m.raw(http.MethodPost, "/v1/admin/invites", "", "application/json", strings.NewReader(`{}`)).status, "merchant keys cannot manage operators")
}

func TestAnalyticsAndSearch(t *testing.T) {
	admin := newAPI(t)
	m, merchantID := admin.merchant("Analytics Kitchen")
	m.post("/v1/payments", map[string]any{"amount": 40000, "currency": "INR", "payment_method": provider.PMCardSuccess, "description": "Paneer tikka platter"})
	m.post("/v1/payments", map[string]any{"amount": 1500, "currency": "INR", "payment_method": provider.PMCardDeclined})

	a := admin.get("/v1/admin/analytics?days=7")
	require.Equal(t, http.StatusOK, a.status, a.body)
	series := a.body["series"].([]any)
	require.Len(t, series, 7, "one point per day, gaps included")
	today := series[6].(map[string]any)
	require.GreaterOrEqual(t, num(today["captured_volume"]), int64(40000))
	cur := a.body["current"].(map[string]any)
	require.Greater(t, cur["success_rate"].(float64), 0.0)
	require.Less(t, cur["success_rate"].(float64), 1.0, "the declined payment counts against the rate")
	require.NotEmpty(t, a.body["statuses"])
	require.Equal(t, http.StatusOK, admin.get("/v1/admin/analytics?days=13").status, "unknown ranges fall back to 30 days")

	byName := admin.get("/v1/admin/search?q=paneer")
	require.Equal(t, http.StatusOK, byName.status, byName.body)
	require.Len(t, byName.body["payments"], 1)
	byMerchant := admin.get("/v1/admin/search?q=analytics%20kitch")
	require.Equal(t, merchantID, byMerchant.body["merchants"].([]any)[0].(map[string]any)["id"])
	byID := admin.get("/v1/admin/search?q=" + merchantID[:8])
	require.NotEmpty(t, byID.body["merchants"])
	require.Empty(t, admin.get("/v1/admin/search?q=x").body["payments"], "too short")
}

func TestErrors(t *testing.T) {
	admin := newAPI(t)
	m, _ := admin.merchant("M")

	noKey := m.do(http.MethodPost, "/v1/payments", "", map[string]any{})
	require.Equal(t, http.StatusBadRequest, noKey.status)
	require.Equal(t, "idempotency_key_required", errCode(noKey))

	unknownField := m.post("/v1/payments", map[string]any{"amount": 1, "currency": "INR", "tip": 5})
	require.Equal(t, "invalid_json", errCode(unknownField))

	invalid := m.post("/v1/payments", map[string]any{"amount": -5, "currency": "INR", "payment_method": "pm_card_success"})
	require.Equal(t, "invalid_request", errCode(invalid))
	require.NotContains(t, invalid.body["error"].(map[string]any)["message"], "ledger:")

	require.Equal(t, http.StatusNotFound, m.get("/v1/payments/"+uuid.NewString()).status)
	require.Equal(t, http.StatusNotFound, m.get("/v1/payments/not-a-uuid").status)

	declined := m.post("/v1/payments", map[string]any{"amount": 500, "currency": "INR", "payment_method": provider.PMCardDeclined})
	require.Equal(t, "failed", declined.body["status"])
	refundFailed := m.post("/v1/payments/"+declined.body["id"].(string)+"/refunds", map[string]any{})
	require.Equal(t, http.StatusConflict, refundFailed.status)
	require.Equal(t, "invalid_state", errCode(refundFailed))

	outage := m.post("/v1/payments", map[string]any{"amount": 500, "currency": "INR", "payment_method": provider.PMCardProviderError})
	require.Equal(t, http.StatusBadGateway, outage.status)
	require.Equal(t, "provider_unavailable", errCode(outage))

	badPeriod := m.get("/v1/merchants/me/statement?from=yesterday")
	require.Equal(t, http.StatusBadRequest, badPeriod.status)
}

// A client hammering "Pay" with the same key is charged once.
func TestDoubleClickProtection(t *testing.T) {
	admin := newAPI(t)
	m, _ := admin.merchant("M")
	key := uuid.NewString()
	body := map[string]any{"amount": 5000, "currency": "INR", "payment_method": provider.PMCardSuccess}

	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]bool{}
	for range 20 {
		wg.Go(func() {
			for {
				r := m.do(http.MethodPost, "/v1/payments", key, body)
				if r.status == http.StatusConflict { // in progress: retry like a real client
					time.Sleep(5 * time.Millisecond)
					continue
				}
				require.Equal(t, http.StatusCreated, r.status, r.body)
				mu.Lock()
				ids[r.body["id"].(string)] = true
				mu.Unlock()
				return
			}
		})
	}
	wg.Wait()
	require.Len(t, ids, 1)
	require.EqualValues(t, 4900, num(m.get("/v1/merchants/me/balance").body["available"]))
}

func TestSettlementImportAPI(t *testing.T) {
	admin := newAPI(t)
	m, _ := admin.merchant("M")
	p := m.post("/v1/payments", map[string]any{"amount": 10000, "currency": "INR", "payment_method": provider.PMCardSuccess})
	csv := fmt.Sprintf("settlement_id,entity_type,entity_id,amount,fee,tax,currency,settled_at\nsetl_9,payment,%s,10000,200,36,INR,%s\n",
		p.body["provider_payment_id"], time.Now().UTC().Format(time.RFC3339))

	first := admin.raw(http.MethodPost, "/v1/admin/settlements?provider=sandbox&source=setl_9.csv", "", "text/csv", strings.NewReader(csv))
	require.Equal(t, http.StatusCreated, first.status, first.body)
	summary := first.body["summary"].(map[string]any)
	require.EqualValues(t, 1, num(summary["matched"]))
	require.EqualValues(t, 10000-236, num(summary["net_to_bank"]))

	again := admin.raw(http.MethodPost, "/v1/admin/settlements?provider=sandbox", "", "text/csv", strings.NewReader(csv))
	require.Equal(t, http.StatusOK, again.status)
	require.Equal(t, first.body["id"], again.body["id"])

	report := admin.get("/v1/admin/settlements/" + first.body["id"].(string))
	require.Equal(t, "matched", report.body["lines"].([]any)[0].(map[string]any)["result"])
	require.Equal(t, http.StatusUnauthorized, m.raw(http.MethodPost, "/v1/admin/settlements?provider=sandbox", "", "text/csv", strings.NewReader(csv)).status)
}

func TestOutboxAdmin(t *testing.T) {
	admin := newAPI(t)
	m, merchantID := admin.merchant("M")
	m.post("/v1/payments", map[string]any{"amount": 1000, "currency": "INR", "payment_method": provider.PMCardSuccess})
	events := admin.get("/v1/admin/outbox?merchant_id=" + merchantID).body["data"].([]any)
	require.Len(t, events, 3)
	var types []string
	for _, e := range events {
		types = append(types, e.(map[string]any)["event_type"].(string))
	}
	require.ElementsMatch(t, []string{"payment.created", "payment.authorized", "payment.captured"}, types)

	id := int64(events[0].(map[string]any)["id"].(float64))
	_, err := pool.Exec(context.Background(), `UPDATE outbox_events SET status = 'dead' WHERE id = $1`, id)
	require.NoError(t, err)
	require.Len(t, admin.get("/v1/admin/outbox?status=dead&merchant_id=" + merchantID).body["data"], 1)
	require.Equal(t, http.StatusOK, admin.post(fmt.Sprintf("/v1/admin/outbox/%d/requeue", id), nil).status)
	require.Equal(t, http.StatusNotFound, admin.post(fmt.Sprintf("/v1/admin/outbox/%d/requeue", id), nil).status, "only dead events")
}

func TestHealthzAndSpec(t *testing.T) {
	c := newAPI(t).as("")
	r := c.get("/healthz")
	require.Equal(t, http.StatusOK, r.status)
	require.Equal(t, "ok", r.body["postgres"])
	spec := c.raw(http.MethodGet, "/openapi.yaml", "", "", nil)
	require.Equal(t, http.StatusOK, spec.status)
}

// Every route the server exposes is documented in openapi.yaml, and the
// spec documents nothing that does not exist.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(openAPISpec, &spec))
	var documented []string
	for path, ops := range spec.Paths {
		for method := range ops {
			documented = append(documented, strings.ToUpper(method)+" "+path)
		}
	}

	srv := &Server{Sandbox: true, Operators: &operators.Service{}, idem: idempotency.NewStore(redis.NewClient(&redis.Options{}))}
	var served []string
	require.NoError(t, chi.Walk(srv.Routes().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		served = append(served, method+" "+strings.TrimSuffix(route, "/"))
		return nil
	}))
	sort.Strings(documented)
	sort.Strings(served)
	require.Equal(t, served, documented)
}

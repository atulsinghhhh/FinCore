package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"fincore/internal/provider"
	"fincore/internal/provider/razorpay/razorpaytest"
)

// newRazorpayAPI returns an admin client for a server whose default
// provider is the fake Razorpay.
func newRazorpayAPI(t *testing.T) (client, *razorpaytest.Fake) {
	t.Helper()
	if pool == nil {
		t.Skip("integration test: run without -short")
	}
	fake := razorpaytest.New()
	t.Cleanup(fake.Close)
	rzp := fake.Client()
	return serve(t, newServer(t, rzp, rzp, provider.NewSandbox())), fake
}

func (c client) webhook(body []byte, signature, eventID string) response {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.url+"/webhooks/razorpay", bytes.NewReader(body))
	require.NoError(c.t, err)
	req.Header.Set("X-Razorpay-Signature", signature)
	req.Header.Del("Authorization")
	req.Header.Set("X-Razorpay-Event-Id", eventID)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	out := response{status: resp.StatusCode}
	_ = json.NewDecoder(resp.Body).Decode(&out.body)
	return out
}

func TestRazorpayWebhookFlow(t *testing.T) {
	admin, fake := newRazorpayAPI(t)
	c, _ := admin.merchant("M")

	created := c.post("/v1/payments", map[string]any{"amount": 50000, "currency": "INR"})
	require.Equal(t, http.StatusCreated, created.status, created.body)
	require.Equal(t, "razorpay", created.body["provider"], "razorpay is this server's default")
	require.Equal(t, "created", created.body["status"])
	next := created.body["next_action"].(map[string]any)
	paymentID := created.body["id"].(string)

	paid := fake.Pay(next["order_id"].(string))
	body, sig := fake.Webhook("payment.authorized", paid["razorpay_payment_id"])

	forged := c.webhook(body, strings.Repeat("0", 64), "evt_forged")
	require.Equal(t, http.StatusUnauthorized, forged.status)
	require.Equal(t, "created", c.get("/v1/payments/" + paymentID).body["status"], "unsigned events change nothing")

	eventID := "evt_" + uuid.NewString()
	first := c.webhook(body, sig, eventID)
	require.Equal(t, http.StatusOK, first.status, first.body)
	require.Equal(t, "processed", first.body["status"])
	require.Equal(t, "captured", c.get("/v1/payments/" + paymentID).body["status"])

	for range 3 {
		dup := c.webhook(body, sig, eventID)
		require.Equal(t, http.StatusOK, dup.status)
		require.Equal(t, "duplicate", dup.body["status"])
	}

	// The browser's confirm arrives after the webhook: same result, no double capture.
	confirm := c.as("").post("/v1/payments/"+paymentID+"/confirm", paid) // from the browser: no API key
	require.Equal(t, http.StatusOK, confirm.status, confirm.body)
	require.Equal(t, "captured", confirm.body["status"])
	require.EqualValues(t, 49000, num(c.get("/v1/merchants/me/balance").body["available"]))

	var count int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT attempts FROM webhook_events WHERE event_id = $1`, eventID).Scan(&count))
	require.Equal(t, 1, count, "duplicates were answered from the Dragonfly cache")
}

func TestRazorpayWebhookDedupeWithoutCache(t *testing.T) {
	admin, fake := newRazorpayAPI(t)
	c, _ := admin.merchant("M")
	created := c.post("/v1/payments", map[string]any{"amount": 10000, "currency": "INR"})
	paid := fake.Pay(created.body["next_action"].(map[string]any)["order_id"].(string))
	body, sig := fake.Webhook("payment.authorized", paid["razorpay_payment_id"])

	eventID := "evt_" + uuid.NewString()
	require.Equal(t, "processed", c.webhook(body, sig, eventID).body["status"])
	require.NoError(t, rdb.Del(t.Context(), "webhook:razorpay:"+eventID).Err()) // Dragonfly lost its data
	again := c.webhook(body, sig, eventID)
	require.Equal(t, http.StatusOK, again.status)
	require.Equal(t, "duplicate", again.body["status"], "Postgres still knows the event was processed")
	require.EqualValues(t, 9800, num(c.get("/v1/merchants/me/balance").body["available"]))
}

func TestRazorpayWebhookTransientFailureIsRetried(t *testing.T) {
	admin, fake := newRazorpayAPI(t)
	c, _ := admin.merchant("M")
	created := c.post("/v1/payments", map[string]any{"amount": 10000, "currency": "INR"})
	paid := fake.Pay(created.body["next_action"].(map[string]any)["order_id"].(string))
	body, sig := fake.Webhook("payment.authorized", paid["razorpay_payment_id"])
	eventID := "evt_" + uuid.NewString()

	fake.FailNext.Store(1) // capture's call to Razorpay fails
	failed := c.webhook(body, sig, eventID)
	require.Equal(t, http.StatusInternalServerError, failed.status, "non-2xx makes Razorpay redeliver")

	retry := c.webhook(body, sig, eventID)
	require.Equal(t, http.StatusOK, retry.status, retry.body)
	require.Equal(t, "processed", retry.body["status"])
	require.Equal(t, "captured", c.get("/v1/payments/" + created.body["id"].(string)).body["status"])
}

func TestDevCheckoutPage(t *testing.T) {
	admin, _ := newRazorpayAPI(t)
	c, _ := admin.merchant("M")
	created := c.post("/v1/payments", map[string]any{"amount": 12345, "currency": "INR"})

	resp, err := http.Get(c.url + "/dev/checkout/" + created.body["id"].(string))
	require.NoError(t, err)
	defer resp.Body.Close()
	html, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(html), "checkout.razorpay.com/v1/checkout.js")
	require.Contains(t, string(html), created.body["next_action"].(map[string]any)["order_id"].(string))
	require.Contains(t, string(html), "₹123.45")
}

// Package razorpaytest is an in-memory fake of the Razorpay API for tests.
// It models orders, payments, captures and refunds, and can play the
// customer (paying in Checkout) and Razorpay (signing webhooks).
package razorpaytest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"fincore/internal/provider/razorpay"
)

type Fake struct {
	KeyID, KeySecret, WebhookSecret string
	Server                          *httptest.Server

	// AutoCapture mimics the Dashboard setting that captures on authorization.
	AutoCapture bool
	// PendingRefunds makes refunds start as "pending" instead of "processed".
	PendingRefunds bool
	// FailNext makes the next N API calls return 503.
	FailNext atomic.Int32
	Calls    atomic.Int32

	mu       sync.Mutex
	prefix   string
	seq      int
	orders   map[string]map[string]any
	payments map[string]map[string]any
	refunds  map[string]map[string]any
}

func New() *Fake {
	f := &Fake{
		KeyID: "rzp_test_fake", KeySecret: "fake_secret", WebhookSecret: "fake_webhook_secret",
		orders: map[string]map[string]any{}, payments: map[string]map[string]any{}, refunds: map[string]map[string]any{},
		// IDs are unique across fakes, as Razorpay's are across orders.
		prefix: strings.ReplaceAll(uuid.NewString(), "-", "")[:8],
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// Client returns a razorpay.Client pointed at the fake.
func (f *Fake) Client() *razorpay.Client {
	c := razorpay.New(f.KeyID, f.KeySecret, f.WebhookSecret)
	c.BaseURL = f.Server.URL
	return c
}

func (f *Fake) Close() { f.Server.Close() }

func (f *Fake) id(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s_%s%06d", prefix, f.prefix, f.seq)
}

// Pay simulates the customer completing Checkout successfully. It returns
// the fields Checkout hands to the browser.
func (f *Fake) Pay(orderID string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.orders[orderID]
	status := "authorized"
	if f.AutoCapture {
		status = "captured"
	}
	id := f.id("pay")
	f.payments[id] = map[string]any{"id": id, "order_id": orderID, "amount": o["amount"], "currency": o["currency"], "status": status}
	o["status"] = "paid"
	return map[string]string{
		"razorpay_payment_id": id,
		"razorpay_order_id":   orderID,
		"razorpay_signature":  razorpay.Sign(f.KeySecret, []byte(orderID+"|"+id)),
	}
}

// FailAttempt simulates a failed customer attempt and returns its payment ID.
func (f *Fake) FailAttempt(orderID, code string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.orders[orderID]
	id := f.id("pay")
	f.payments[id] = map[string]any{"id": id, "order_id": orderID, "amount": o["amount"], "currency": o["currency"], "status": "failed", "error_code": code}
	return id
}

func (f *Fake) PaymentStatus(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.payments[id]["status"].(string)
}

// Webhook builds a signed webhook for a payment or refund event.
func (f *Fake) Webhook(event, entityID string) (body []byte, signature string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	payload := map[string]any{}
	if strings.HasPrefix(event, "refund.") {
		r := f.refunds[entityID]
		payload["refund"] = map[string]any{"entity": r}
		payload["payment"] = map[string]any{"entity": f.payments[r["payment_id"].(string)]}
	} else {
		payload["payment"] = map[string]any{"entity": f.payments[entityID]}
	}
	body, _ = json.Marshal(map[string]any{"entity": "event", "event": event, "payload": payload})
	return body, razorpay.Sign(f.WebhookSecret, body)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.Calls.Add(1)
	if user, pass, ok := r.BasicAuth(); !ok || user != f.KeyID || pass != f.KeySecret {
		apiError(w, http.StatusUnauthorized, "BAD_REQUEST_ERROR", "Authentication failed")
		return
	}
	if f.FailNext.Load() > 0 {
		f.FailNext.Add(-1)
		apiError(w, http.StatusServiceUnavailable, "SERVER_ERROR", "temporarily unavailable")
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPost && len(parts) == 1 && parts[0] == "orders":
		if body["amount"].(float64) < 100 {
			apiError(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "Order amount less than minimum amount allowed")
			return
		}
		id := f.id("order")
		f.orders[id] = map[string]any{"id": id, "amount": int64(body["amount"].(float64)), "currency": body["currency"], "status": "created"}
		writeJSON(w, f.orders[id])

	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "orders" && parts[2] == "payments":
		items := []map[string]any{}
		for _, p := range f.payments {
			if p["order_id"] == parts[1] {
				items = append(items, p)
			}
		}
		writeJSON(w, map[string]any{"count": len(items), "items": items})

	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "payments":
		p, ok := f.payments[parts[1]]
		if !ok {
			apiError(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "The id provided does not exist")
			return
		}
		writeJSON(w, p)

	case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "capture":
		p, ok := f.payments[parts[1]]
		switch {
		case !ok:
			apiError(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "The id provided does not exist")
		case p["status"] != "authorized":
			apiError(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "This payment has already been captured")
		case int64(body["amount"].(float64)) != toInt(p["amount"]):
			apiError(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "Capture amount must be equal to the amount authorized")
		default:
			p["status"] = "captured"
			writeJSON(w, p)
		}

	case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "refund":
		p, ok := f.payments[parts[1]]
		if !ok || (p["status"] != "captured" && p["status"] != "refunded") {
			apiError(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "Payment not captured")
			return
		}
		status := "processed"
		if f.PendingRefunds {
			status = "pending"
		}
		id := f.id("rfnd")
		f.refunds[id] = map[string]any{"id": id, "payment_id": p["id"], "amount": int64(body["amount"].(float64)), "status": status}
		writeJSON(w, f.refunds[id])

	default:
		apiError(w, http.StatusNotFound, "NOT_FOUND", "no route")
	}
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "description": desc}})
}

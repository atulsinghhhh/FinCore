// Package razorpay implements provider.Provider for Razorpay.
//
// Flow: CreatePayment creates a Razorpay order. The customer pays in Razorpay
// Checkout, which returns a signed {payment_id, order_id, signature} to the
// browser; the client forwards it to /confirm (Confirm). Independently,
// Razorpay sends signed webhooks (ParseWebhook). Either path may arrive
// first, or twice; the payments service handles both idempotently.
package razorpay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"fincore/internal/provider"
)

const DefaultBaseURL = "https://api.razorpay.com/v1"

type Client struct {
	KeyID         string
	keySecret     string
	webhookSecret string
	BaseURL       string
	HTTP          *http.Client
}

var (
	_ provider.Provider      = (*Client)(nil)
	_ provider.Confirmer     = (*Client)(nil)
	_ provider.StatusChecker = (*Client)(nil)
)

func New(keyID, keySecret, webhookSecret string) *Client {
	return &Client{
		KeyID:         keyID,
		keySecret:     keySecret,
		webhookSecret: webhookSecret,
		BaseURL:       DefaultBaseURL,
		HTTP:          &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) Name() string { return "razorpay" }

type order struct {
	ID       string `json:"id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}

type payment struct {
	ID          string  `json:"id"`
	OrderID     string  `json:"order_id"`
	Amount      int64   `json:"amount"`
	Currency    string  `json:"currency"`
	Status      string  `json:"status"` // created, authorized, captured, refunded, failed
	ErrorCode   *string `json:"error_code"`
	ErrorReason *string `json:"error_reason"`
}

type refund struct {
	ID        string `json:"id"`
	PaymentID string `json:"payment_id"`
	Amount    int64  `json:"amount"`
	Status    string `json:"status"` // pending, processed, failed
}

// CreatePayment creates an order. Razorpay payments always need the
// customer to act in Checkout, so the outcome is requires_action.
func (c *Client) CreatePayment(ctx context.Context, r provider.CreateRequest) (provider.CreateResult, error) {
	var o order
	err := c.do(ctx, http.MethodPost, "/orders", map[string]any{
		"amount":   r.Amount,
		"currency": r.Currency,
		"receipt":  r.PaymentID.String(),
		"notes":    map[string]string{"fincore_payment_id": r.PaymentID.String()},
	}, &o)
	if err != nil {
		return provider.CreateResult{}, err
	}
	next, _ := json.Marshal(map[string]any{
		"type":     "razorpay_checkout",
		"key_id":   c.KeyID,
		"order_id": o.ID,
		"amount":   o.Amount,
		"currency": o.Currency,
	})
	return provider.CreateResult{Outcome: provider.OutcomeRequiresAction, Reference: o.ID, NextAction: next}, nil
}

// Confirm verifies the Checkout signature, then asks Razorpay for the
// payment's real state; the browser is never trusted for status or amount.
func (c *Client) Confirm(ctx context.Context, orderID string, params map[string]string) (provider.ConfirmResult, error) {
	paymentID, sig := params["razorpay_payment_id"], params["razorpay_signature"]
	if paymentID == "" || sig == "" || params["razorpay_order_id"] != orderID {
		return provider.ConfirmResult{}, fmt.Errorf("%w: missing or mismatched checkout fields", provider.ErrInvalidSignature)
	}
	if !validHMAC(c.keySecret, []byte(orderID+"|"+paymentID), sig) {
		return provider.ConfirmResult{}, provider.ErrInvalidSignature
	}
	p, err := c.fetchPayment(ctx, paymentID)
	if err != nil {
		return provider.ConfirmResult{}, err
	}
	if p.OrderID != orderID {
		return provider.ConfirmResult{}, fmt.Errorf("%w: payment belongs to another order", provider.ErrInvalidSignature)
	}
	res := provider.ConfirmResult{PaymentRef: p.ID, Amount: p.Amount}
	switch p.Status {
	case "authorized":
		res.Outcome = provider.OutcomeAuthorized
	case "captured", "refunded":
		res.Outcome, res.Captured = provider.OutcomeAuthorized, true
	case "failed":
		res.Outcome, res.DeclineCode = provider.OutcomeDeclined, deref(p.ErrorCode, "payment_failed")
	default:
		return provider.ConfirmResult{}, fmt.Errorf("%w: payment is still %s", provider.ErrRejected, p.Status)
	}
	return res, nil
}

// CheckPayment lists the order's payment attempts and reports a successful
// one, if any.
func (c *Client) CheckPayment(ctx context.Context, orderID string) (provider.ConfirmResult, bool, error) {
	var list struct {
		Items []payment `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/orders/"+url.PathEscape(orderID)+"/payments", nil, &list); err != nil {
		return provider.ConfirmResult{}, false, err
	}
	for _, p := range list.Items {
		switch p.Status {
		case "authorized", "captured", "refunded":
			return provider.ConfirmResult{
				PaymentRef: p.ID, Outcome: provider.OutcomeAuthorized, Amount: p.Amount,
				Captured: p.Status != "authorized",
			}, true, nil
		}
	}
	return provider.ConfirmResult{}, false, nil
}

// Capture is idempotent: if the account auto-captures, or a previous attempt
// succeeded, there is nothing to do.
func (c *Client) Capture(ctx context.Context, paymentRef string, amount int64, currency string) error {
	p, err := c.fetchPayment(ctx, paymentRef)
	if err != nil {
		return err
	}
	if p.Status == "captured" || p.Status == "refunded" {
		return nil
	}
	return c.do(ctx, http.MethodPost, "/payments/"+url.PathEscape(paymentRef)+"/capture",
		map[string]any{"amount": amount, "currency": currency}, nil)
}

// Void is a no-op: Razorpay has no API to release an authorization. Uncaptured
// payments are refunded to the customer automatically after the window set
// in the Dashboard (Settings → Payment capture).
func (c *Client) Void(context.Context, string) error { return nil }

func (c *Client) Refund(ctx context.Context, paymentRef string, refundID uuid.UUID, amount int64) (provider.RefundResult, error) {
	var r refund
	err := c.do(ctx, http.MethodPost, "/payments/"+url.PathEscape(paymentRef)+"/refund", map[string]any{
		"amount":  amount,
		"receipt": refundID.String(),
		"notes":   map[string]string{"fincore_refund_id": refundID.String()},
	}, &r)
	if err != nil {
		return provider.RefundResult{}, err
	}
	return provider.RefundResult{Reference: r.ID, Pending: r.Status != "processed"}, nil
}

func (c *Client) fetchPayment(ctx context.Context, id string) (payment, error) {
	var p payment
	err := c.do(ctx, http.MethodGet, "/payments/"+url.PathEscape(id), nil, &p)
	return p, err
}

// VerifyWebhook checks the X-Razorpay-Signature header against the raw body.
func (c *Client) VerifyWebhook(body []byte, signature string) bool {
	return c.webhookSecret != "" && validHMAC(c.webhookSecret, body, signature)
}

type webhook struct {
	Event   string `json:"event"`
	Payload struct {
		Payment *struct{ Entity payment } `json:"payment"`
		Refund  *struct{ Entity refund }  `json:"refund"`
	} `json:"payload"`
}

// ParseWebhook translates a verified webhook body into a provider event.
// eventID comes from the X-Razorpay-Event-Id header.
func ParseWebhook(eventID string, body []byte) (provider.Event, error) {
	var w webhook
	if err := json.Unmarshal(body, &w); err != nil {
		return provider.Event{}, fmt.Errorf("%w: %v", provider.ErrRejected, err)
	}
	ev := provider.Event{ID: eventID, RawType: w.Event, Type: provider.EventIgnored}
	if p := w.Payload.Payment; p != nil {
		ev.Reference, ev.PaymentRef, ev.Amount = p.Entity.OrderID, p.Entity.ID, p.Entity.Amount
		ev.DeclineCode = deref(p.Entity.ErrorCode, "")
	}
	switch w.Event {
	case "payment.authorized":
		ev.Type = provider.EventPaymentAuthorized
	case "payment.captured":
		ev.Type = provider.EventPaymentCaptured
	case "payment.failed":
		ev.Type = provider.EventPaymentFailed
		if ev.DeclineCode == "" {
			ev.DeclineCode = "payment_failed"
		}
	case "refund.processed", "refund.failed":
		if w.Payload.Refund == nil {
			return provider.Event{}, fmt.Errorf("%w: %s without refund entity", provider.ErrRejected, w.Event)
		}
		ev.RefundRef, ev.Amount = w.Payload.Refund.Entity.ID, w.Payload.Refund.Entity.Amount
		ev.Type = provider.EventRefundProcessed
		if w.Event == "refund.failed" {
			ev.Type = provider.EventRefundFailed
		}
	}
	return ev, nil
}

type apiError struct {
	Error struct {
		Code        string `json:"code"`
		Description string `json:"description"`
	} `json:"error"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.KeyID, c.keySecret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", provider.ErrUnavailable, err)
	}
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: razorpay %s %s returned %d", provider.ErrUnavailable, method, path, resp.StatusCode)
	case resp.StatusCode >= 400:
		var e apiError
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("%w: razorpay %s: %s", provider.ErrRejected, e.Error.Code, e.Error.Description)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: decode razorpay response: %v", provider.ErrUnavailable, err)
		}
	}
	return nil
}

func validHMAC(secret string, msg []byte, signature string) bool {
	want, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(msg)
	return hmac.Equal(mac.Sum(nil), want)
}

// Sign returns the hex HMAC-SHA256 Razorpay would send. For tests.
func Sign(secret string, msg []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(msg)
	return hex.EncodeToString(mac.Sum(nil))
}

func deref(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

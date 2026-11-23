// Package provider abstracts the external payment processor (Razorpay,
// Stripe, ...). The payments service only talks to these interfaces, so
// processors are swappable and tests can run against the sandbox.
package provider

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

var (
	// ErrUnavailable means the processor could not be reached or returned a
	// server error. The operation may or may not have happened; callers must
	// leave state where a retry or reconciliation can resolve it.
	ErrUnavailable = errors.New("provider: unavailable")
	// ErrRejected means the processor refused the request (4xx). Retrying
	// the same request will not help.
	ErrRejected             = errors.New("provider: request rejected")
	ErrUnknownPaymentMethod = errors.New("provider: unknown payment method")
	ErrInvalidSignature     = errors.New("provider: invalid signature")
)

type Outcome string

const (
	OutcomeAuthorized Outcome = "authorized"
	OutcomeDeclined   Outcome = "declined"
	// OutcomeRequiresAction means the customer must complete the payment in
	// the provider's checkout; the result arrives later via Confirm or a
	// webhook.
	OutcomeRequiresAction Outcome = "requires_action"
)

type CreateRequest struct {
	PaymentID     uuid.UUID
	Amount        int64
	Currency      string
	PaymentMethod string
	Description   string
}

type CreateResult struct {
	Outcome Outcome
	// Reference is the provider's ID for the payment as a whole (an order).
	Reference string
	// PaymentRef is the ID of the customer's payment attempt, once known.
	PaymentRef  string
	DeclineCode string
	// NextAction is what the client needs to finish a requires_action
	// payment, e.g. the checkout key and order ID.
	NextAction json.RawMessage
}

type RefundResult struct {
	Reference string
	// Pending means the provider accepted the refund but has not settled it;
	// a webhook will report the final result.
	Pending bool
}

// Provider operations must be idempotent per reference: retrying a capture,
// void or refund with the same arguments must not repeat it.
type Provider interface {
	Name() string
	CreatePayment(ctx context.Context, r CreateRequest) (CreateResult, error)
	Capture(ctx context.Context, paymentRef string, amount int64, currency string) error
	Void(ctx context.Context, paymentRef string) error
	Refund(ctx context.Context, paymentRef string, refundID uuid.UUID, amount int64) (RefundResult, error)
}

type ConfirmResult struct {
	PaymentRef  string
	Outcome     Outcome // authorized or declined
	Captured    bool
	Amount      int64
	DeclineCode string
}

// Confirmer is implemented by providers whose checkout returns a signed
// result to the browser, which the client forwards to us.
type Confirmer interface {
	Confirm(ctx context.Context, reference string, params map[string]string) (ConfirmResult, error)
}

// StatusChecker lets the sweeper ask the provider directly about a payment
// whose confirm and webhooks may both have been lost. found is false when
// the customer has not successfully paid.
type StatusChecker interface {
	CheckPayment(ctx context.Context, reference string) (res ConfirmResult, found bool, err error)
}

type EventType string

const (
	EventPaymentAuthorized EventType = "payment.authorized"
	EventPaymentCaptured   EventType = "payment.captured"
	EventPaymentFailed     EventType = "payment.failed"
	EventRefundProcessed   EventType = "refund.processed"
	EventRefundFailed      EventType = "refund.failed"
	EventIgnored           EventType = "ignored"
)

// Event is a provider webhook translated into provider-neutral terms.
type Event struct {
	ID          string
	Type        EventType
	RawType     string
	Reference   string // order
	PaymentRef  string
	RefundRef   string
	Amount      int64
	DeclineCode string
}

package provider

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Sandbox test payment methods, modelled on processor test cards.
const (
	PMCardSuccess           = "pm_card_success"
	PMCardDeclined          = "pm_card_declined"
	PMCardInsufficientFunds = "pm_card_insufficient_funds"
	PMCardProviderError     = "pm_card_provider_error"
)

// Sandbox is an in-process processor for local development and tests. It
// authorizes synchronously based on the payment method.
type Sandbox struct{}

var _ Provider = (*Sandbox)(nil)

func NewSandbox() *Sandbox { return &Sandbox{} }

func (*Sandbox) Name() string { return "sandbox" }

func (*Sandbox) CreatePayment(_ context.Context, r CreateRequest) (CreateResult, error) {
	ref := "sbx_" + r.PaymentID.String()
	switch r.PaymentMethod {
	case PMCardSuccess:
		return CreateResult{Outcome: OutcomeAuthorized, Reference: ref, PaymentRef: ref}, nil
	case PMCardDeclined:
		return CreateResult{Outcome: OutcomeDeclined, Reference: ref, DeclineCode: "card_declined"}, nil
	case PMCardInsufficientFunds:
		return CreateResult{Outcome: OutcomeDeclined, Reference: ref, DeclineCode: "insufficient_funds"}, nil
	case PMCardProviderError:
		return CreateResult{}, fmt.Errorf("%w: sandbox simulated outage", ErrUnavailable)
	}
	return CreateResult{}, fmt.Errorf("%w: %q", ErrUnknownPaymentMethod, r.PaymentMethod)
}

func (*Sandbox) Capture(context.Context, string, int64, string) error { return nil }

func (*Sandbox) Void(context.Context, string) error { return nil }

func (*Sandbox) Refund(_ context.Context, _ string, refundID uuid.UUID, _ int64) (RefundResult, error) {
	return RefundResult{Reference: "sbx_rfnd_" + refundID.String()}, nil
}

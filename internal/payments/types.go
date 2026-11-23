// Package payments implements the payment, refund and payout lifecycles.
// Every money movement is posted to the ledger in the same database
// transaction as the state change that caused it.
package payments

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"fincore/internal/ledger"
)

// MaxAmount caps a single payment or payout (₹1,000 crore in paise) so fee
// arithmetic can never overflow int64.
const MaxAmount int64 = 1_000_000_000_000

type Merchant struct {
	ID               uuid.UUID `json:"id"`
	Name             string    `json:"name"`
	Currency         string    `json:"currency"`
	FeeBps           int32     `json:"fee_bps"`
	PayableAccountID uuid.UUID `json:"payable_account_id"`
	WebhookURL       *string   `json:"webhook_url"`
	CreatedAt        time.Time `json:"created_at"`
}

type WebhookEndpoint struct {
	URL string `json:"url"`
	// Secret is shown once; merchants use it to verify FinCore-Signature.
	Secret string `json:"secret"`
}

type MerchantBalance struct {
	MerchantID uuid.UUID `json:"merchant_id"`
	Currency   string    `json:"currency"`
	// Available is what the merchant can be paid out now.
	Available int64 `json:"available"`
	// PendingPayouts is held in transit to the merchant's bank.
	PendingPayouts int64 `json:"pending_payouts"`
}

type Payment struct {
	ID            uuid.UUID `json:"id"`
	MerchantID    uuid.UUID `json:"merchant_id"`
	Amount        int64     `json:"amount"`
	Currency      string    `json:"currency"`
	Status        Status    `json:"status"`
	CaptureMethod string    `json:"capture_method"`
	PaymentMethod string    `json:"payment_method"`
	Description   string    `json:"description"`
	Provider      string    `json:"provider"`
	ProviderRef   *string   `json:"provider_ref"`
	// ProviderPaymentID is the provider's ID for the customer's successful
	// payment attempt (Razorpay pay_...).
	ProviderPaymentID *string `json:"provider_payment_id"`
	// NextAction tells the client how to finish a created payment, e.g.
	// open Razorpay Checkout with this order.
	NextAction     json.RawMessage `json:"next_action,omitempty"`
	AmountCaptured int64           `json:"amount_captured"`
	AmountRefunded int64           `json:"amount_refunded"`
	FeeAmount      int64           `json:"fee_amount"`
	FailureReason  *string         `json:"failure_reason"`
	CaptureTxnID   *uuid.UUID      `json:"capture_ledger_txn_id"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Replayed       bool            `json:"-"`
}

type PaymentEvent struct {
	ID          int64      `json:"id"`
	FromStatus  *string    `json:"from_status"`
	ToStatus    string     `json:"to_status"`
	Amount      *int64     `json:"amount"`
	LedgerTxnID *uuid.UUID `json:"ledger_txn_id"`
	Note        string     `json:"note"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Refund struct {
	ID               uuid.UUID  `json:"id"`
	PaymentID        uuid.UUID  `json:"payment_id"`
	Amount           int64      `json:"amount"`
	Currency         string     `json:"currency"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	ProviderRefundID *string    `json:"provider_refund_id"`
	FailureReason    *string    `json:"failure_reason"`
	LedgerTxnID      uuid.UUID  `json:"ledger_txn_id"`
	ReversalTxnID    *uuid.UUID `json:"reversal_ledger_txn_id"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Replayed         bool       `json:"-"`
}

type Payout struct {
	ID            uuid.UUID  `json:"id"`
	MerchantID    uuid.UUID  `json:"merchant_id"`
	Amount        int64      `json:"amount"`
	Currency      string     `json:"currency"`
	Status        string     `json:"status"`
	FailureReason *string    `json:"failure_reason"`
	HoldTxnID     uuid.UUID  `json:"hold_ledger_txn_id"`
	SettleTxnID   *uuid.UUID `json:"settle_ledger_txn_id"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Replayed      bool       `json:"-"`
}

type CreateMerchantRequest struct {
	Name     string `json:"name"`
	Currency string `json:"currency"`
	FeeBps   int32  `json:"fee_bps"`
}

type CreatePaymentRequest struct {
	// MerchantID comes from the API key, never from the request body.
	MerchantID     uuid.UUID `json:"-"`
	IdempotencyKey string    `json:"-"`
	Amount         int64     `json:"amount"`
	Currency       string    `json:"currency"`
	// CaptureMethod is "automatic" (default) or "manual" (authorize now,
	// capture later).
	CaptureMethod string `json:"capture_method"`
	// PaymentMethod is a sandbox test method; Razorpay customers choose
	// theirs in Checkout, so it is optional there.
	PaymentMethod string `json:"payment_method"`
	Description   string `json:"description"`
	// Provider defaults to the service's default provider.
	Provider string `json:"provider"`
}

type CaptureRequest struct {
	// Amount defaults to the full authorized amount. Less captures partially.
	Amount *int64 `json:"amount"`
}

type RefundRequest struct {
	IdempotencyKey string `json:"-"`
	// Amount defaults to everything still refundable.
	Amount *int64 `json:"amount"`
	Reason string `json:"reason"`
}

type CreatePayoutRequest struct {
	MerchantID     uuid.UUID `json:"-"`
	IdempotencyKey string    `json:"-"`
	Amount         int64     `json:"amount"`
}

var (
	ErrNotFound             = errors.New("payments: not found")
	ErrInvalidTransition    = errors.New("payments: invalid state transition")
	ErrProviderUnavailable  = errors.New("payments: payment provider unavailable")
	ErrInvalidRequest       = ledger.ErrInvalidRequest
	ErrIdempotencyKeyReused = ledger.ErrIdempotencyKeyReused
	ErrInsufficientFunds    = ledger.ErrInsufficientFunds
)

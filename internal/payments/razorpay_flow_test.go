package payments

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"fincore/internal/provider"
	"fincore/internal/provider/razorpay"
	"fincore/internal/provider/razorpay/razorpaytest"
)

type rzpFixture struct {
	fixture
	fake *razorpaytest.Fake
}

func setupRazorpay(t *testing.T) rzpFixture {
	t.Helper()
	base := setup(t)
	fake := razorpaytest.New()
	t.Cleanup(fake.Close)
	base.svc = NewService(testPool, base.ledger, provider.NewSandbox(), fake.Client())
	return rzpFixture{fixture: base, fake: fake}
}

// start creates a Razorpay payment and returns it with its order ID.
func (f rzpFixture) start(t *testing.T, amount int64, opts ...func(*CreatePaymentRequest)) (Payment, string) {
	t.Helper()
	p := f.pay(t, amount, append([]func(*CreatePaymentRequest){func(r *CreatePaymentRequest) {
		r.Provider, r.PaymentMethod = "razorpay", ""
	}}, opts...)...)
	require.Equal(t, StatusCreated, p.Status)
	var next struct {
		Type    string `json:"type"`
		OrderID string `json:"order_id"`
	}
	require.NoError(t, json.Unmarshal(p.NextAction, &next))
	require.Equal(t, "razorpay_checkout", next.Type)
	return p, next.OrderID
}

func (f rzpFixture) webhook(t *testing.T, event, entityID string) error {
	t.Helper()
	body, _ := f.fake.Webhook(event, entityID)
	ev, err := razorpay.ParseWebhook(uuid.NewString(), body)
	require.NoError(t, err)
	return f.svc.ApplyProviderEvent(context.Background(), "razorpay", ev)
}

func TestRazorpayCheckoutConfirmFlow(t *testing.T) {
	f := setupRazorpay(t)
	ctx := context.Background()
	p, order := f.start(t, 100000)
	require.Zero(t, f.balance(t).Available, "no money until the customer pays")

	params := f.fake.Pay(order)
	confirmed, err := f.svc.ConfirmPayment(ctx, p.ID, params)
	require.NoError(t, err)
	require.Equal(t, StatusCaptured, confirmed.Status)
	require.Equal(t, params["razorpay_payment_id"], *confirmed.ProviderPaymentID)
	require.Nil(t, confirmed.NextAction, "next_action is only shown while awaiting the customer")
	require.Equal(t, "captured", f.fake.PaymentStatus(params["razorpay_payment_id"]))
	require.EqualValues(t, 98000, f.balance(t).Available)

	// The browser retries and Razorpay's webhooks arrive late: nothing doubles.
	_, err = f.svc.ConfirmPayment(ctx, p.ID, params)
	require.NoError(t, err)
	require.NoError(t, f.webhook(t, "payment.authorized", params["razorpay_payment_id"]))
	require.NoError(t, f.webhook(t, "payment.captured", params["razorpay_payment_id"]))
	require.EqualValues(t, 98000, f.balance(t).Available)
	f.requireLedgerHealthy(t)
}

func TestRazorpayTamperedConfirmIsRejected(t *testing.T) {
	f := setupRazorpay(t)
	p, order := f.start(t, 5000)
	params := f.fake.Pay(order)
	params["razorpay_signature"] = razorpay.Sign("attacker", []byte(order))
	_, err := f.svc.ConfirmPayment(context.Background(), p.ID, params)
	require.ErrorIs(t, err, ErrInvalidRequest)
	got, err := f.svc.GetPayment(context.Background(), p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCreated, got.Status)
}

func TestRazorpayWebhookArrivesBeforeConfirm(t *testing.T) {
	f := setupRazorpay(t)
	f.fake.AutoCapture = true
	p, order := f.start(t, 20000)
	params := f.fake.Pay(order)

	require.NoError(t, f.webhook(t, "payment.captured", params["razorpay_payment_id"]))
	got, err := f.svc.GetPayment(context.Background(), p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCaptured, got.Status)

	confirmed, err := f.svc.ConfirmPayment(context.Background(), p.ID, params)
	require.NoError(t, err)
	require.Equal(t, StatusCaptured, confirmed.Status)
	require.EqualValues(t, 19600, f.balance(t).Available)
}

func TestRazorpayFailedAttemptThenSuccess(t *testing.T) {
	f := setupRazorpay(t)
	p, order := f.start(t, 30000)

	failed := f.fake.FailAttempt(order, "BAD_REQUEST_ERROR")
	require.NoError(t, f.webhook(t, "payment.failed", failed))
	got, err := f.svc.GetPayment(context.Background(), p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCreated, got.Status, "the customer can still retry the same order")
	require.Equal(t, "BAD_REQUEST_ERROR", *got.FailureReason)

	paid := f.fake.Pay(order)["razorpay_payment_id"]
	require.NoError(t, f.webhook(t, "payment.authorized", paid))
	got, err = f.svc.GetPayment(context.Background(), p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCaptured, got.Status)
	require.Nil(t, got.FailureReason)
}

func TestRazorpayManualCaptureCapturedAtProvider(t *testing.T) {
	f := setupRazorpay(t)
	f.fake.AutoCapture = true // the merchant's Razorpay account auto-captures
	p, order := f.start(t, 10000, manual)
	paid := f.fake.Pay(order)["razorpay_payment_id"]

	require.NoError(t, f.webhook(t, "payment.captured", paid))
	got, err := f.svc.GetPayment(context.Background(), p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCaptured, got.Status, "money captured at the provider must reach the ledger")
	require.EqualValues(t, 9800, f.balance(t).Available)
}

func TestRazorpayOutageDuringCaptureRollsBack(t *testing.T) {
	f := setupRazorpay(t)
	ctx := context.Background()
	p, order := f.start(t, 10000, manual)
	_, err := f.svc.ConfirmPayment(ctx, p.ID, f.fake.Pay(order))
	require.NoError(t, err)

	f.fake.FailNext.Store(1)
	_, err = f.svc.Capture(ctx, p.ID, CaptureRequest{})
	require.ErrorIs(t, err, ErrProviderUnavailable)
	got, err := f.svc.GetPayment(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusAuthorized, got.Status)
	require.Zero(t, f.balance(t).Available, "the ledger posting rolled back with the failed capture")

	_, err = f.svc.Capture(ctx, p.ID, CaptureRequest{})
	require.NoError(t, err, "a retry succeeds")
	require.EqualValues(t, 9800, f.balance(t).Available)
}

func TestRazorpayOutageDuringRefundRollsBack(t *testing.T) {
	f := setupRazorpay(t)
	ctx := context.Background()
	p, order := f.start(t, 10000)
	_, err := f.svc.ConfirmPayment(ctx, p.ID, f.fake.Pay(order))
	require.NoError(t, err)

	f.fake.FailNext.Store(1)
	_, err = f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: uuid.NewString()})
	require.ErrorIs(t, err, ErrProviderUnavailable)
	require.EqualValues(t, 9800, f.balance(t).Available)
	got, err := f.svc.GetPayment(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCaptured, got.Status)
	f.requireLedgerHealthy(t)
}

func TestRefundAfterPayoutNeverReachesProvider(t *testing.T) {
	f := setupRazorpay(t)
	ctx := context.Background()
	p, order := f.start(t, 10000)
	_, err := f.svc.ConfirmPayment(ctx, p.ID, f.fake.Pay(order))
	require.NoError(t, err)
	_, err = f.svc.CreatePayout(ctx, CreatePayoutRequest{MerchantID: f.merchant.ID, IdempotencyKey: uuid.NewString(), Amount: 9800})
	require.NoError(t, err)

	calls := f.fake.Calls.Load()
	_, err = f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: uuid.NewString()})
	require.ErrorIs(t, err, ErrInsufficientFunds)
	require.Equal(t, calls, f.fake.Calls.Load(), "the ledger rejects it before any money moves at Razorpay")
}

func TestRazorpayPendingRefunds(t *testing.T) {
	f := setupRazorpay(t)
	ctx := context.Background()
	f.fake.PendingRefunds = true
	p, order := f.start(t, 100000)
	_, err := f.svc.ConfirmPayment(ctx, p.ID, f.fake.Pay(order))
	require.NoError(t, err)

	ok, err := f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: uuid.NewString(), Amount: ptr(int64(30000))})
	require.NoError(t, err)
	require.Equal(t, RefundPending, ok.Status)
	require.NoError(t, f.webhook(t, "refund.processed", *ok.ProviderRefundID))

	bad, err := f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: uuid.NewString()}) // remaining 70,000
	require.NoError(t, err)
	got, _ := f.svc.GetPayment(ctx, p.ID)
	require.Equal(t, StatusRefunded, got.Status)
	require.EqualValues(t, 0, f.balance(t).Available)

	require.NoError(t, f.webhook(t, "refund.failed", *bad.ProviderRefundID))
	require.NoError(t, f.webhook(t, "refund.failed", *bad.ProviderRefundID), "duplicate delivery is a no-op")

	refunds, err := f.svc.ListRefunds(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, RefundSucceeded, refunds[0].Status)
	require.Equal(t, RefundFailed, refunds[1].Status)
	require.NotNil(t, refunds[1].ReversalTxnID)

	got, _ = f.svc.GetPayment(ctx, p.ID)
	require.Equal(t, StatusPartiallyRefunded, got.Status)
	require.EqualValues(t, 30000, got.AmountRefunded)
	// 98,000 earned - (30,000 - 600 fee share) refunded = 68,600
	require.EqualValues(t, 68600, f.balance(t).Available, "the failed refund's money is back with the merchant")
	f.requireLedgerHealthy(t)
}

func TestRazorpayRejectedOrderFailsPayment(t *testing.T) {
	f := setupRazorpay(t)
	p := f.pay(t, 50, func(r *CreatePaymentRequest) { r.Provider, r.PaymentMethod = "razorpay", "" })
	require.Equal(t, StatusFailed, p.Status)
	require.Equal(t, "provider_rejected", *p.FailureReason)
}

func TestEventsForUnknownPaymentsAreIgnored(t *testing.T) {
	f := setupRazorpay(t)
	err := f.svc.ApplyProviderEvent(context.Background(), "razorpay", provider.Event{
		Type: provider.EventPaymentAuthorized, Reference: "order_not_ours", Amount: 100,
	})
	require.NoError(t, err)
	require.NoError(t, f.svc.ApplyProviderEvent(context.Background(), "razorpay", provider.Event{
		Type: provider.EventRefundFailed, RefundRef: "rfnd_not_ours",
	}))
}

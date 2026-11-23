package razorpay_test

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

func setup(t *testing.T) (*razorpaytest.Fake, *razorpay.Client) {
	f := razorpaytest.New()
	t.Cleanup(f.Close)
	return f, f.Client()
}

func createOrder(t *testing.T, c *razorpay.Client, amount int64) string {
	res, err := c.CreatePayment(context.Background(), provider.CreateRequest{PaymentID: uuid.New(), Amount: amount, Currency: "INR"})
	require.NoError(t, err)
	require.Equal(t, provider.OutcomeRequiresAction, res.Outcome)
	return res.Reference
}

func TestCreatePaymentReturnsCheckoutAction(t *testing.T) {
	_, c := setup(t)
	res, err := c.CreatePayment(context.Background(), provider.CreateRequest{PaymentID: uuid.New(), Amount: 50000, Currency: "INR"})
	require.NoError(t, err)
	var next map[string]any
	require.NoError(t, json.Unmarshal(res.NextAction, &next))
	require.Equal(t, "razorpay_checkout", next["type"])
	require.Equal(t, res.Reference, next["order_id"])
	require.Equal(t, "rzp_test_fake", next["key_id"])
}

func TestConfirmVerifiesSignatureAndFetchesTruth(t *testing.T) {
	f, c := setup(t)
	ctx := context.Background()
	order := createOrder(t, c, 50000)
	params := f.Pay(order)

	res, err := c.Confirm(ctx, order, params)
	require.NoError(t, err)
	require.Equal(t, provider.OutcomeAuthorized, res.Outcome)
	require.Equal(t, params["razorpay_payment_id"], res.PaymentRef)
	require.EqualValues(t, 50000, res.Amount)

	tampered := map[string]string{}
	for k, v := range params {
		tampered[k] = v
	}
	tampered["razorpay_signature"] = razorpay.Sign("wrong-secret", []byte("x"))
	_, err = c.Confirm(ctx, order, tampered)
	require.ErrorIs(t, err, provider.ErrInvalidSignature)

	other := createOrder(t, c, 50000)
	_, err = c.Confirm(ctx, other, params)
	require.ErrorIs(t, err, provider.ErrInvalidSignature, "a signature for one order must not confirm another")
}

func TestCaptureIsIdempotent(t *testing.T) {
	f, c := setup(t)
	ctx := context.Background()
	payID := f.Pay(createOrder(t, c, 10000))["razorpay_payment_id"]

	require.NoError(t, c.Capture(ctx, payID, 10000, "INR"))
	require.Equal(t, "captured", f.PaymentStatus(payID))
	require.NoError(t, c.Capture(ctx, payID, 10000, "INR"), "second capture is a no-op")

	f.AutoCapture = true
	auto := f.Pay(createOrder(t, c, 10000))["razorpay_payment_id"]
	require.NoError(t, c.Capture(ctx, auto, 10000, "INR"), "auto-captured payments need no capture call")
}

func TestErrorMapping(t *testing.T) {
	f, c := setup(t)
	ctx := context.Background()

	_, err := c.CreatePayment(ctx, provider.CreateRequest{PaymentID: uuid.New(), Amount: 50, Currency: "INR"})
	require.ErrorIs(t, err, provider.ErrRejected, "4xx is a rejection")

	f.FailNext.Store(1)
	_, err = c.CreatePayment(ctx, provider.CreateRequest{PaymentID: uuid.New(), Amount: 5000, Currency: "INR"})
	require.ErrorIs(t, err, provider.ErrUnavailable, "5xx is retryable")

	bad := razorpay.New("rzp_test_fake", "wrong", "")
	bad.BaseURL = f.Server.URL
	_, err = bad.CreatePayment(ctx, provider.CreateRequest{PaymentID: uuid.New(), Amount: 5000, Currency: "INR"})
	require.ErrorIs(t, err, provider.ErrRejected)
}

func TestRefund(t *testing.T) {
	f, c := setup(t)
	ctx := context.Background()
	payID := f.Pay(createOrder(t, c, 10000))["razorpay_payment_id"]
	require.NoError(t, c.Capture(ctx, payID, 10000, "INR"))

	res, err := c.Refund(ctx, payID, uuid.New(), 4000)
	require.NoError(t, err)
	require.False(t, res.Pending)

	f.PendingRefunds = true
	res, err = c.Refund(ctx, payID, uuid.New(), 1000)
	require.NoError(t, err)
	require.True(t, res.Pending)
}

func TestWebhookVerificationAndParsing(t *testing.T) {
	f, c := setup(t)
	order := createOrder(t, c, 25000)
	payID := f.Pay(order)["razorpay_payment_id"]

	body, sig := f.Webhook("payment.authorized", payID)
	require.True(t, c.VerifyWebhook(body, sig))
	require.False(t, c.VerifyWebhook(append(body, ' '), sig), "any change to the body breaks the signature")
	require.False(t, c.VerifyWebhook(body, "not-hex"))
	require.False(t, razorpay.New("k", "s", "").VerifyWebhook(body, sig), "no secret configured means nothing verifies")

	ev, err := razorpay.ParseWebhook("evt_1", body)
	require.NoError(t, err)
	require.Equal(t, provider.EventPaymentAuthorized, ev.Type)
	require.Equal(t, order, ev.Reference)
	require.Equal(t, payID, ev.PaymentRef)
	require.EqualValues(t, 25000, ev.Amount)

	failedID := f.FailAttempt(order, "BAD_REQUEST_ERROR")
	body, _ = f.Webhook("payment.failed", failedID)
	ev, err = razorpay.ParseWebhook("evt_2", body)
	require.NoError(t, err)
	require.Equal(t, provider.EventPaymentFailed, ev.Type)
	require.Equal(t, "BAD_REQUEST_ERROR", ev.DeclineCode)

	ev, err = razorpay.ParseWebhook("evt_3", []byte(`{"event":"order.paid","payload":{}}`))
	require.NoError(t, err)
	require.Equal(t, provider.EventIgnored, ev.Type)
}

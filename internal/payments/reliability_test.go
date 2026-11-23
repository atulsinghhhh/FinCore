package payments

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"fincore/internal/provider"
	"fincore/internal/provider/razorpay"
)

func age(t *testing.T, paymentID uuid.UUID, d time.Duration) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `UPDATE payments SET created_at = created_at - $2::interval WHERE id = $1`, paymentID, d.String())
	require.NoError(t, err)
}

func TestSweeperRecoversPaymentWithLostCallbacks(t *testing.T) {
	f := setupRazorpay(t)
	ctx := context.Background()
	p, order := f.start(t, 25000)
	f.fake.Pay(order) // customer paid, then closed the tab; webhook never arrived
	age(t, p.ID, 5*time.Minute)

	res, err := f.svc.SweepStalePayments(ctx, 2*time.Minute, 30*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, res.Recovered)
	got, err := f.svc.GetPayment(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCaptured, got.Status)
	require.EqualValues(t, 24500, f.balance(t).Available)
}

func TestSweeperExpiresUnpaidPayments(t *testing.T) {
	f := setupRazorpay(t)
	ctx := context.Background()
	fresh, _ := f.start(t, 1000)
	old, _ := f.start(t, 1000)
	checkedOnly, _ := f.start(t, 1000)
	age(t, old.ID, time.Hour)
	age(t, checkedOnly.ID, 5*time.Minute)

	res, err := f.svc.SweepStalePayments(ctx, 2*time.Minute, 30*time.Minute)
	require.NoError(t, err)
	require.Equal(t, SweepResult{Expired: 1}, res)

	for id, want := range map[uuid.UUID]Status{fresh.ID: StatusCreated, old.ID: StatusFailed, checkedOnly.ID: StatusCreated} {
		got, err := f.svc.GetPayment(ctx, id)
		require.NoError(t, err)
		require.Equal(t, want, got.Status)
		if want == StatusFailed {
			require.Equal(t, "expired", *got.FailureReason)
			require.Nil(t, got.NextAction)
		}
	}
}

func TestSweeperDoesNotExpireWhenProviderIsDown(t *testing.T) {
	f := setupRazorpay(t)
	p, _ := f.start(t, 1000)
	age(t, p.ID, time.Hour)
	f.fake.FailNext.Store(1)

	res, err := f.svc.SweepStalePayments(context.Background(), 2*time.Minute, 30*time.Minute)
	require.NoError(t, err)
	require.Zero(t, res.Expired, "never expire a payment whose real state we could not check")
}

func TestWebhookRetryWorker(t *testing.T) {
	f := setupRazorpay(t)
	ctx := context.Background()
	p, order := f.start(t, 10000)
	paid := f.fake.Pay(order)
	body, _ := f.fake.Webhook("payment.authorized", paid["razorpay_payment_id"])
	ev, err := razorpay.ParseWebhook("evt_"+uuid.NewString(), body)
	require.NoError(t, err)

	row, done, err := f.svc.BeginWebhook(ctx, "razorpay", ev, body)
	require.NoError(t, err)
	require.False(t, done)
	f.fake.FailNext.Store(1)
	procErr := f.svc.ApplyProviderEvent(ctx, "razorpay", ev)
	require.ErrorIs(t, procErr, ErrProviderUnavailable)
	require.NoError(t, f.svc.FinishWebhook(ctx, row.ID, row.Attempts, procErr))

	_, err = testPool.Exec(ctx, `UPDATE webhook_events SET next_retry_at = now() WHERE id = $1`, row.ID) // skip the backoff wait
	require.NoError(t, err)
	parsers := map[string]func(string, []byte) (provider.Event, error){"razorpay": razorpay.ParseWebhook}
	found, err := f.svc.RetryWebhook(ctx, parsers)
	require.NoError(t, err)
	require.True(t, found)

	got, err := f.svc.GetPayment(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCaptured, got.Status)
	var processed bool
	require.NoError(t, testPool.QueryRow(ctx, `SELECT processed_at IS NOT NULL FROM webhook_events WHERE id = $1`, row.ID).Scan(&processed))
	require.True(t, processed)

	found, err = f.svc.RetryWebhook(ctx, parsers)
	require.NoError(t, err)
	require.False(t, found, "nothing left to retry")
}

func TestBackoff(t *testing.T) {
	for attempt, max := range map[int]time.Duration{0: 5 * time.Second, 3: 40 * time.Second, 20: time.Hour} {
		for range 50 {
			d := Backoff(attempt)
			require.GreaterOrEqual(t, d, max/2)
			require.Less(t, d, max)
		}
	}
}

func TestNoOutboxEventsFromRolledBackOperations(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	_, err := f.svc.CreatePayout(ctx, CreatePayoutRequest{MerchantID: f.merchant.ID, IdempotencyKey: uuid.NewString(), Amount: 1})
	require.ErrorIs(t, err, ErrInsufficientFunds)
	var n int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE merchant_id = $1`, f.merchant.ID).Scan(&n))
	require.Zero(t, n)
}

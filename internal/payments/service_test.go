package payments

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"fincore/internal/ledger"
	"fincore/internal/provider"
	"fincore/internal/store"
	"fincore/internal/testutil"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	pool, cleanup, err := testutil.StartPostgres(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

type fixture struct {
	svc      *Service
	ledger   *ledger.Ledger
	merchant Merchant
}

func setup(t *testing.T) fixture {
	t.Helper()
	if testPool == nil {
		t.Skip("integration test: run without -short")
	}
	l := ledger.New(testPool)
	svc := NewService(testPool, l, provider.NewSandbox())
	m, err := svc.CreateMerchant(context.Background(), CreateMerchantRequest{Name: "Chai Point", Currency: "INR", FeeBps: 200})
	require.NoError(t, err)
	return fixture{svc: svc, ledger: l, merchant: m}
}

func (f fixture) pay(t *testing.T, amount int64, opts ...func(*CreatePaymentRequest)) Payment {
	t.Helper()
	req := CreatePaymentRequest{
		MerchantID: f.merchant.ID, IdempotencyKey: uuid.NewString(), Amount: amount,
		Currency: "INR", PaymentMethod: provider.PMCardSuccess,
	}
	for _, o := range opts {
		o(&req)
	}
	p, err := f.svc.CreatePayment(context.Background(), req)
	require.NoError(t, err)
	return p
}

func manual(r *CreatePaymentRequest) { r.CaptureMethod = "manual" }

func (f fixture) balance(t *testing.T) MerchantBalance {
	t.Helper()
	b, err := f.svc.MerchantBalance(context.Background(), f.merchant.ID)
	require.NoError(t, err)
	return b
}

func (f fixture) requireLedgerHealthy(t *testing.T) {
	t.Helper()
	report, err := f.ledger.CheckIntegrity(context.Background())
	require.NoError(t, err)
	require.True(t, report.OK(), "%+v", report)
}

func ptr[T any](v T) *T { return &v }

func TestAutomaticCapture(t *testing.T) {
	f := setup(t)
	p := f.pay(t, 100000)

	require.Equal(t, StatusCaptured, p.Status)
	require.EqualValues(t, 100000, p.AmountCaptured)
	require.EqualValues(t, 2000, p.FeeAmount)
	require.NotNil(t, p.CaptureTxnID)
	require.EqualValues(t, 98000, f.balance(t).Available)

	events, err := f.svc.ListPaymentEvents(context.Background(), p.ID)
	require.NoError(t, err)
	var path []string
	for _, e := range events {
		path = append(path, e.ToStatus)
	}
	require.Equal(t, []string{"created", "authorized", "captured"}, path)
	f.requireLedgerHealthy(t)
}

func TestDeclinedPaymentMovesNoMoney(t *testing.T) {
	f := setup(t)
	p := f.pay(t, 5000, func(r *CreatePaymentRequest) { r.PaymentMethod = provider.PMCardDeclined })
	require.Equal(t, StatusFailed, p.Status)
	require.Equal(t, "card_declined", *p.FailureReason)
	require.Nil(t, p.CaptureTxnID)
	require.Zero(t, f.balance(t).Available)
}

func TestManualCaptureAndPartialCapture(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := f.pay(t, 50000, manual)
	require.Equal(t, StatusAuthorized, p.Status)
	require.Zero(t, f.balance(t).Available, "authorization alone moves no money")

	_, err := f.svc.Capture(ctx, p.ID, CaptureRequest{Amount: ptr(int64(50001))})
	require.ErrorIs(t, err, ErrInvalidRequest)

	p, err = f.svc.Capture(ctx, p.ID, CaptureRequest{Amount: ptr(int64(30000))})
	require.NoError(t, err)
	require.EqualValues(t, 30000, p.AmountCaptured)
	require.EqualValues(t, 600, p.FeeAmount)
	require.EqualValues(t, 29400, f.balance(t).Available)

	_, err = f.svc.Capture(ctx, p.ID, CaptureRequest{})
	require.ErrorIs(t, err, ErrInvalidTransition, "cannot capture twice")
}

func TestCancel(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := f.pay(t, 1000, manual)
	p, err := f.svc.Cancel(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCanceled, p.Status)

	_, err = f.svc.Capture(ctx, p.ID, CaptureRequest{})
	require.ErrorIs(t, err, ErrInvalidTransition)
	_, err = f.svc.Cancel(ctx, f.pay(t, 1000).ID)
	require.ErrorIs(t, err, ErrInvalidTransition, "cannot cancel a captured payment")
}

func TestConcurrentCapturesPostOnce(t *testing.T) {
	f := setup(t)
	p := f.pay(t, 10000, manual)

	var wg sync.WaitGroup
	var ok, conflict atomic.Int32
	for range 20 {
		wg.Go(func() {
			_, err := f.svc.Capture(context.Background(), p.ID, CaptureRequest{})
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrInvalidTransition):
				conflict.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, ok.Load())
	require.EqualValues(t, 19, conflict.Load())
	require.EqualValues(t, 9800, f.balance(t).Available)
}

func TestRefunds(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := f.pay(t, 100000)

	key := uuid.NewString()
	r1, err := f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: key, Amount: ptr(int64(40000)), Reason: "damaged"})
	require.NoError(t, err)
	require.EqualValues(t, 40000, r1.Amount)

	again, err := f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: key, Amount: ptr(int64(40000)), Reason: "damaged"})
	require.NoError(t, err)
	require.True(t, again.Replayed)
	require.Equal(t, r1.ID, again.ID)

	_, err = f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: key, Amount: ptr(int64(1))})
	require.ErrorIs(t, err, ErrIdempotencyKeyReused)

	got, err := f.svc.GetPayment(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusPartiallyRefunded, got.Status)
	require.EqualValues(t, 40000, got.AmountRefunded)

	_, err = f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: uuid.NewString(), Amount: ptr(int64(60001))})
	require.ErrorIs(t, err, ErrInvalidRequest, "cannot refund more than captured")

	_, err = f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: uuid.NewString()}) // the rest
	require.NoError(t, err)
	got, err = f.svc.GetPayment(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusRefunded, got.Status)

	// Full refund: the fee came back pro rata, so everyone is back to zero.
	require.Zero(t, f.balance(t).Available)
	refunds, err := f.svc.ListRefunds(ctx, p.ID)
	require.NoError(t, err)
	require.Len(t, refunds, 2)
	f.requireLedgerHealthy(t)
}

func TestProRataFeeSumsExactly(t *testing.T) {
	// 3 equal refunds of an amount that does not divide evenly.
	fee, captured := int64(2001), int64(100003)
	var returned, refunded int64
	for _, amt := range []int64{33334, 33334, 33335} {
		returned += proRata(fee, refunded+amt, captured) - proRata(fee, refunded, captured)
		refunded += amt
	}
	require.Equal(t, fee, returned)
	require.EqualValues(t, 0, proRata(5, 1, 0))
}

func TestFeeRounding(t *testing.T) {
	require.EqualValues(t, 2000, Fee(100000, 200))
	require.EqualValues(t, 1, Fee(25, 200), "0.5 rounds up")
	require.EqualValues(t, 0, Fee(24, 200))
	require.EqualValues(t, 0, Fee(100000, 0))
	require.EqualValues(t, MaxAmount, Fee(MaxAmount, 10000))
}

func TestRefundAfterPayoutNeedsFunds(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := f.pay(t, 10000)
	_, err := f.svc.CreatePayout(ctx, CreatePayoutRequest{MerchantID: f.merchant.ID, IdempotencyKey: uuid.NewString(), Amount: 9800})
	require.NoError(t, err)

	_, err = f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: uuid.NewString()})
	require.ErrorIs(t, err, ErrInsufficientFunds)
	got, err := f.svc.GetPayment(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCaptured, got.Status, "failed refund must not change the payment")
}

func TestCreatePaymentIdempotency(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	req := CreatePaymentRequest{
		MerchantID: f.merchant.ID, IdempotencyKey: uuid.NewString(), Amount: 7500,
		Currency: "INR", PaymentMethod: provider.PMCardSuccess,
	}
	first, err := f.svc.CreatePayment(ctx, req)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			again, err := f.svc.CreatePayment(ctx, req)
			require.NoError(t, err)
			require.Equal(t, first.ID, again.ID)
			require.True(t, again.Replayed)
		})
	}
	wg.Wait()
	require.EqualValues(t, 7350, f.balance(t).Available, "charged exactly once")

	req.Amount = 7501
	_, err = f.svc.CreatePayment(ctx, req)
	require.ErrorIs(t, err, ErrIdempotencyKeyReused)
}

func TestProviderOutageThenRetryResumes(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	req := CreatePaymentRequest{
		MerchantID: f.merchant.ID, IdempotencyKey: uuid.NewString(), Amount: 1000,
		Currency: "INR", PaymentMethod: provider.PMCardProviderError,
	}
	_, err := f.svc.CreatePayment(ctx, req)
	require.ErrorIs(t, err, ErrProviderUnavailable)

	var p store.Payment
	p, err = f.svc.q.GetPaymentByIdempotencyKey(ctx, store.GetPaymentByIdempotencyKeyParams{MerchantID: f.merchant.ID, IdempotencyKey: req.IdempotencyKey})
	require.NoError(t, err)
	require.Equal(t, string(StatusCreated), p.Status, "outage leaves the payment retryable")

	// Same request again: the stored payment resumes authorization. The
	// sandbox still fails, proving the retry reached the provider again.
	_, err = f.svc.CreatePayment(ctx, req)
	require.ErrorIs(t, err, ErrProviderUnavailable)
}

func TestValidation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	base := CreatePaymentRequest{MerchantID: f.merchant.ID, IdempotencyKey: "k", Amount: 100, Currency: "INR", PaymentMethod: provider.PMCardSuccess}
	for name, mutate := range map[string]func(*CreatePaymentRequest){
		"zero amount":      func(r *CreatePaymentRequest) { r.Amount = 0 },
		"too large":        func(r *CreatePaymentRequest) { r.Amount = MaxAmount + 1 },
		"wrong currency":   func(r *CreatePaymentRequest) { r.Currency = "USD" },
		"bad capture":      func(r *CreatePaymentRequest) { r.CaptureMethod = "later" },
		"unknown provider": func(r *CreatePaymentRequest) { r.Provider = "paypal" },
		"no key":           func(r *CreatePaymentRequest) { r.IdempotencyKey = "" },
	} {
		r := base
		r.IdempotencyKey = uuid.NewString()
		mutate(&r)
		_, err := f.svc.CreatePayment(ctx, r)
		require.ErrorIs(t, err, ErrInvalidRequest, name)
	}
	_, err := f.svc.CreatePayment(ctx, CreatePaymentRequest{MerchantID: uuid.New(), IdempotencyKey: "k", Amount: 1, Currency: "INR", PaymentMethod: "x"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestPayouts(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.pay(t, 100000) // merchant has 98,000

	_, err := f.svc.CreatePayout(ctx, CreatePayoutRequest{MerchantID: f.merchant.ID, IdempotencyKey: uuid.NewString(), Amount: 98001})
	require.ErrorIs(t, err, ErrInsufficientFunds)

	key := uuid.NewString()
	paid, err := f.svc.CreatePayout(ctx, CreatePayoutRequest{MerchantID: f.merchant.ID, IdempotencyKey: key, Amount: 50000})
	require.NoError(t, err)
	require.Equal(t, PayoutPending, paid.Status)
	replay, err := f.svc.CreatePayout(ctx, CreatePayoutRequest{MerchantID: f.merchant.ID, IdempotencyKey: key, Amount: 50000})
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, paid.ID, replay.ID)

	b := f.balance(t)
	require.EqualValues(t, 48000, b.Available)
	require.EqualValues(t, 50000, b.PendingPayouts)

	paid, err = f.svc.CompletePayout(ctx, paid.ID)
	require.NoError(t, err)
	require.Equal(t, PayoutPaid, paid.Status)
	_, err = f.svc.FailPayout(ctx, paid.ID, "")
	require.ErrorIs(t, err, ErrInvalidTransition, "a paid payout cannot fail later")

	failed, err := f.svc.CreatePayout(ctx, CreatePayoutRequest{MerchantID: f.merchant.ID, IdempotencyKey: uuid.NewString(), Amount: 48000})
	require.NoError(t, err)
	require.EqualValues(t, 0, f.balance(t).Available)
	failed, err = f.svc.FailPayout(ctx, failed.ID, "invalid_ifsc")
	require.NoError(t, err)
	require.Equal(t, "invalid_ifsc", *failed.FailureReason)

	b = f.balance(t)
	require.EqualValues(t, 48000, b.Available, "failed payout returns the money")
	require.Zero(t, b.PendingPayouts)
	f.requireLedgerHealthy(t)
}

func TestConcurrentPayoutsNeverOverdraw(t *testing.T) {
	f := setup(t)
	f.pay(t, 10000) // 9,800 available

	var wg sync.WaitGroup
	var ok atomic.Int32
	for range 30 {
		wg.Go(func() {
			_, err := f.svc.CreatePayout(context.Background(), CreatePayoutRequest{MerchantID: f.merchant.ID, IdempotencyKey: uuid.NewString(), Amount: 1000})
			if err == nil {
				ok.Add(1)
			} else if !errors.Is(err, ErrInsufficientFunds) {
				t.Errorf("unexpected: %v", err)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 9, ok.Load())
	b := f.balance(t)
	require.EqualValues(t, 800, b.Available)
	require.EqualValues(t, 9000, b.PendingPayouts)
}

// The Go state machine and the database trigger must agree exactly.
func TestTransitionsMatchDatabase(t *testing.T) {
	f := setup(t)
	rows, err := f.svc.q.ListStatusTransitions(context.Background())
	require.NoError(t, err)
	var db []string
	for _, r := range rows {
		db = append(db, r.Entity+":"+r.FromStatus+"->"+r.ToStatus)
	}
	var code []string
	for from, tos := range paymentTransitions {
		for _, to := range tos {
			code = append(code, "payment:"+string(from)+"->"+string(to))
		}
	}
	for from, tos := range payoutTransitions {
		for _, to := range tos {
			code = append(code, "payout:"+from+"->"+to)
		}
	}
	for from, tos := range refundTransitions {
		for _, to := range tos {
			code = append(code, "refund:"+from+"->"+to)
		}
	}
	sort.Strings(db)
	sort.Strings(code)
	require.Equal(t, db, code)
}

func TestDatabaseRejectsIllegalTransition(t *testing.T) {
	f := setup(t)
	p := f.pay(t, 1000)
	_, err := testPool.Exec(context.Background(), `UPDATE payments SET status = 'authorized' WHERE id = $1`, p.ID)
	require.ErrorIs(t, mapError(err), ErrInvalidTransition, "captured -> authorized must be refused by the database")
}

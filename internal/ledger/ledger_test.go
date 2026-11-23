package ledger

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// The ₹1,000 payment example from the docs: customer pays, platform keeps
// 2%, provider settles to the bank, merchant is paid out.
func TestPaymentLifecycle(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)

	pspClearing := newAccount(t, l, Asset, true)
	bank := newAccount(t, l, Asset, false)
	merchant := newAccount(t, l, Liability, false)
	fees := newAccount(t, l, Revenue, false)

	payment, err := l.Post(ctx, PostRequest{
		IdempotencyKey: uuid.NewString(),
		Description:    "Payment pay_123",
		Postings: []PostingInput{
			{AccountID: pspClearing.ID, Direction: Debit, Amount: 100000, Currency: "INR"},
			{AccountID: merchant.ID, Direction: Credit, Amount: 98000, Currency: "INR"},
			{AccountID: fees.ID, Direction: Credit, Amount: 2000, Currency: "INR"},
		},
	})
	require.NoError(t, err)
	require.Len(t, payment.Postings, 3)
	require.False(t, payment.Replayed)

	_, err = l.Post(ctx, PostRequest{
		IdempotencyKey: uuid.NewString(),
		Description:    "Provider settlement",
		Postings: []PostingInput{
			{AccountID: bank.ID, Direction: Debit, Amount: 100000, Currency: "INR"},
			{AccountID: pspClearing.ID, Direction: Credit, Amount: 100000, Currency: "INR"},
		},
	})
	require.NoError(t, err)

	_, err = l.Post(ctx, PostRequest{
		IdempotencyKey: uuid.NewString(),
		Description:    "Merchant payout",
		Postings: []PostingInput{
			{AccountID: merchant.ID, Direction: Debit, Amount: 98000, Currency: "INR"},
			{AccountID: bank.ID, Direction: Credit, Amount: 98000, Currency: "INR"},
		},
	})
	require.NoError(t, err)

	require.EqualValues(t, 0, balanceOf(t, l, pspClearing))
	require.EqualValues(t, 2000, balanceOf(t, l, bank))
	require.EqualValues(t, 0, balanceOf(t, l, merchant))
	require.EqualValues(t, 2000, balanceOf(t, l, fees))
	requireIntegrity(t, l)
}

func TestDatabaseRejectsUnbalancedTransactions(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	cash := newAccount(t, l, Asset, true)
	float := newAccount(t, l, Asset, true)
	wallet := newAccount(t, l, Liability, false)

	// Bypass the Go validation entirely: the database itself must refuse.
	insertRaw := func(postings ...[2]any) error {
		tx, err := testPool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx)
		var id uuid.UUID
		err = tx.QueryRow(ctx,
			`INSERT INTO ledger_transactions (idempotency_key, request_hash, description)
			 VALUES ($1, 'raw', 'raw insert') RETURNING id`, uuid.NewString()).Scan(&id)
		require.NoError(t, err)
		for _, p := range postings {
			_, err = tx.Exec(ctx,
				`INSERT INTO postings (transaction_id, account_id, currency, direction, amount)
				 VALUES ($1, $2, 'INR', $3, $4)`, id, p[0], p[1], 100)
			if err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}

	requireCode := func(err error, code string) {
		var pgErr *pgconn.PgError
		require.True(t, errors.As(err, &pgErr), "want postgres error %s, got %v", code, err)
		require.Equal(t, code, pgErr.Code, pgErr.Message)
	}

	requireCode(insertRaw(), "FC001")                                                      // no postings
	requireCode(insertRaw([2]any{cash.ID, "debit"}), "FC001")                              // one-sided
	requireCode(insertRaw([2]any{cash.ID, "debit"}, [2]any{float.ID, "debit"}), "FC001")   // debit/debit
	requireCode(insertRaw([2]any{cash.ID, "credit"}, [2]any{wallet.ID, "debit"}), "23514") // overdraws empty wallet
	require.NoError(t, insertRaw([2]any{cash.ID, "debit"}, [2]any{wallet.ID, "credit"}))   // balanced
	requireIntegrity(t, l)
}

func TestLedgerIsAppendOnly(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	cash := newAccount(t, l, Asset, true)
	wallet := newAccount(t, l, Liability, false)
	txn, err := l.Post(ctx, PostRequest{
		IdempotencyKey: uuid.NewString(),
		Description:    "top-up",
		Postings: []PostingInput{
			{AccountID: cash.ID, Direction: Debit, Amount: 500, Currency: "INR"},
			{AccountID: wallet.ID, Direction: Credit, Amount: 500, Currency: "INR"},
		},
	})
	require.NoError(t, err)

	for _, stmt := range []string{
		`UPDATE postings SET amount = 1 WHERE transaction_id = $1`,
		`DELETE FROM postings WHERE transaction_id = $1`,
		`UPDATE ledger_transactions SET description = 'edited' WHERE id = $1`,
		`DELETE FROM ledger_transactions WHERE id = $1`,
	} {
		_, err := testPool.Exec(ctx, stmt, txn.ID)
		require.ErrorIs(t, MapError(err), ErrImmutable, stmt)
	}
	_, err = testPool.Exec(ctx, `TRUNCATE postings, ledger_transactions CASCADE`)
	require.ErrorIs(t, MapError(err), ErrImmutable)

	_, err = testPool.Exec(ctx, `UPDATE accounts SET currency = 'USD' WHERE id = $1`, wallet.ID)
	require.ErrorIs(t, MapError(err), ErrImmutable)
	_, err = testPool.Exec(ctx, `UPDATE accounts SET name = 'renamed' WHERE id = $1`, wallet.ID)
	require.NoError(t, err, "non-financial fields stay editable")

	require.EqualValues(t, 500, balanceOf(t, l, wallet))
}

func TestIdempotentReplay(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	alice := newAccount(t, l, Liability, true)
	bob := newAccount(t, l, Liability, false)

	req := PostRequest{IdempotencyKey: uuid.NewString(), Description: "p2p", Postings: transfer(alice, bob, 700)}
	first, err := l.Post(ctx, req)
	require.NoError(t, err)

	for range 10 {
		again, err := l.Post(ctx, req)
		require.NoError(t, err)
		require.True(t, again.Replayed)
		require.Equal(t, first.ID, again.ID)
	}
	require.EqualValues(t, 700, balanceOf(t, l, bob), "retries must not move money twice")

	changed := req
	changed.Postings = transfer(alice, bob, 701)
	_, err = l.Post(ctx, changed)
	require.ErrorIs(t, err, ErrIdempotencyKeyReused)
}

func TestConcurrentRequestsWithSameIdempotencyKey(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	alice := newAccount(t, l, Liability, true)
	bob := newAccount(t, l, Liability, false)
	req := PostRequest{IdempotencyKey: uuid.NewString(), Description: "double click", Postings: transfer(alice, bob, 100)}

	const n = 25
	var wg sync.WaitGroup
	var fresh atomic.Int32
	ids := make([]uuid.UUID, n)
	for i := range n {
		wg.Go(func() {
			txn, err := l.Post(ctx, req)
			require.NoError(t, err)
			ids[i] = txn.ID
			if !txn.Replayed {
				fresh.Add(1)
			}
		})
	}
	wg.Wait()

	require.EqualValues(t, 1, fresh.Load(), "exactly one request should post")
	for _, id := range ids {
		require.Equal(t, ids[0], id)
	}
	require.EqualValues(t, 100, balanceOf(t, l, bob))
}

func TestInsufficientFundsWritesNothing(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	wallet := newAccount(t, l, Liability, false)
	merchant := newAccount(t, l, Liability, false)

	before, err := l.Balance(ctx, wallet.ID)
	require.NoError(t, err)

	key := uuid.NewString()
	_, err = l.Post(ctx, PostRequest{IdempotencyKey: key, Description: "overdraw", Postings: transfer(wallet, merchant, 1)})
	require.ErrorIs(t, err, ErrInsufficientFunds)

	after, err := l.Balance(ctx, wallet.ID)
	require.NoError(t, err)
	require.Equal(t, before.Version, after.Version)
	var count int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`, key).Scan(&count))
	require.Zero(t, count, "a failed post must roll back completely, freeing the key for a retry")
}

func TestCurrencyMismatch(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	inr := newAccount(t, l, Liability, true)
	usd, err := l.CreateAccount(ctx, NewAccount{Code: "usd:" + uuid.NewString(), Name: "usd", Type: Liability, Currency: "USD"})
	require.NoError(t, err)

	_, err = l.Post(ctx, PostRequest{
		IdempotencyKey: uuid.NewString(),
		Description:    "wrong currency",
		Postings: []PostingInput{
			{AccountID: inr.ID, Direction: Debit, Amount: 100, Currency: "USD"},
			{AccountID: usd.ID, Direction: Credit, Amount: 100, Currency: "USD"},
		},
	})
	require.ErrorIs(t, err, ErrCurrencyMismatch)
}

func TestUnknownAccount(t *testing.T) {
	l := newTestLedger(t)
	alice := newAccount(t, l, Liability, true)
	ghost := Account{ID: uuid.New(), Type: Liability}
	_, err := l.Post(ctxT(t), PostRequest{IdempotencyKey: uuid.NewString(), Description: "x", Postings: transfer(alice, ghost, 1)})
	require.ErrorIs(t, err, ErrAccountNotFound)
}

func TestDuplicateAccountCode(t *testing.T) {
	l := newTestLedger(t)
	a := newAccount(t, l, Asset, false)
	_, err := l.CreateAccount(ctxT(t), NewAccount{Code: a.Code, Name: "dup", Type: Asset, Currency: "INR"})
	require.ErrorIs(t, err, ErrDuplicateAccountCode)
}

func TestReverse(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	alice := newAccount(t, l, Liability, true)
	bob := newAccount(t, l, Liability, false)

	original, err := l.Post(ctx, PostRequest{IdempotencyKey: uuid.NewString(), Description: "p2p", Postings: transfer(alice, bob, 450)})
	require.NoError(t, err)

	key := uuid.NewString()
	reversal, err := l.Reverse(ctx, ReverseRequest{TransactionID: original.ID, IdempotencyKey: key})
	require.NoError(t, err)
	require.NotNil(t, reversal.ReversesID)
	require.Equal(t, original.ID, *reversal.ReversesID)
	require.EqualValues(t, 0, balanceOf(t, l, alice))
	require.EqualValues(t, 0, balanceOf(t, l, bob))

	replayed, err := l.Reverse(ctx, ReverseRequest{TransactionID: original.ID, IdempotencyKey: key})
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, reversal.ID, replayed.ID)

	_, err = l.Reverse(ctx, ReverseRequest{TransactionID: original.ID, IdempotencyKey: uuid.NewString()})
	require.ErrorIs(t, err, ErrAlreadyReversed)

	// The original is untouched: history is never rewritten.
	reloaded, err := l.GetTransaction(ctx, original.ID)
	require.NoError(t, err)
	require.Equal(t, original.Postings, reloaded.Postings)
	requireIntegrity(t, l)
}

func TestReverseRespectsFunds(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	alice := newAccount(t, l, Liability, true)
	bob := newAccount(t, l, Liability, false)
	carol := newAccount(t, l, Liability, false)

	original, err := l.Post(ctx, PostRequest{IdempotencyKey: uuid.NewString(), Description: "to bob", Postings: transfer(alice, bob, 300)})
	require.NoError(t, err)
	_, err = l.Post(ctx, PostRequest{IdempotencyKey: uuid.NewString(), Description: "bob spends", Postings: transfer(bob, carol, 300)})
	require.NoError(t, err)

	_, err = l.Reverse(ctx, ReverseRequest{TransactionID: original.ID, IdempotencyKey: uuid.NewString()})
	require.ErrorIs(t, err, ErrInsufficientFunds, "cannot claw back money bob already spent")
}

func TestBalanceAt(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	alice := newAccount(t, l, Liability, true)
	bob := newAccount(t, l, Liability, false)

	jan := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	feb := time.Date(2026, 2, 15, 10, 0, 0, 0, time.UTC)
	for _, p := range []struct {
		at     time.Time
		amount int64
	}{{jan, 1000}, {feb, 250}} {
		_, err := l.Post(ctx, PostRequest{IdempotencyKey: uuid.NewString(), Description: "credit", EffectiveAt: p.at, Postings: transfer(alice, bob, p.amount)})
		require.NoError(t, err)
	}

	cases := []struct {
		at   time.Time
		want int64
	}{
		{jan.Add(-time.Second), 0},
		{jan, 1000},
		{feb.Add(-time.Second), 1000},
		{feb, 1250},
	}
	for _, c := range cases {
		b, err := l.BalanceAt(ctx, bob.ID, c.at)
		require.NoError(t, err)
		require.EqualValues(t, c.want, b.Balance, "balance at %s", c.at)
	}
	require.EqualValues(t, 1250, balanceOf(t, l, bob))
}

// 100 concurrent ₹1 debits against a ₹50 balance: exactly 50 succeed and the
// balance never goes negative.
func TestConcurrentDebitsNeverOverdraw(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	funding := newAccount(t, l, Liability, true)
	wallet := newAccount(t, l, Liability, false)
	merchant := newAccount(t, l, Liability, false)
	_, err := l.Post(ctx, PostRequest{IdempotencyKey: uuid.NewString(), Description: "fund", Postings: transfer(funding, wallet, 50)})
	require.NoError(t, err)

	var wg sync.WaitGroup
	var ok, rejected atomic.Int32
	for range 100 {
		wg.Go(func() {
			_, err := l.Post(ctx, PostRequest{IdempotencyKey: uuid.NewString(), Description: "spend", Postings: transfer(wallet, merchant, 1)})
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrInsufficientFunds):
				rejected.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()

	require.EqualValues(t, 50, ok.Load())
	require.EqualValues(t, 50, rejected.Load())
	require.EqualValues(t, 0, balanceOf(t, l, wallet))
	require.EqualValues(t, 50, balanceOf(t, l, merchant))
	requireIntegrity(t, l)
}

// Opposing transfers (A→B and B→A) lock the same two rows. Ordered locking
// means they queue instead of deadlocking.
func TestOpposingTransfersDoNotDeadlock(t *testing.T) {
	l := newTestLedger(t)
	ctx := ctxT(t)
	a := newAccount(t, l, Liability, true)
	b := newAccount(t, l, Liability, true)

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			from, to := a, b
			if i%2 == 1 {
				from, to = b, a
			}
			_, err := l.Post(ctx, PostRequest{IdempotencyKey: uuid.NewString(), Description: "ping-pong", Postings: transfer(from, to, 10)})
			require.NoError(t, err)
		})
	}
	wg.Wait()

	require.EqualValues(t, 0, balanceOf(t, l, a))
	require.EqualValues(t, 0, balanceOf(t, l, b))
	requireIntegrity(t, l)
}

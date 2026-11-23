package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"fincore/internal/store"
)

type Ledger struct {
	pool *pgxpool.Pool
	q    *store.Queries
	now  func() time.Time
}

func New(pool *pgxpool.Pool) *Ledger {
	return &Ledger{pool: pool, q: store.New(pool), now: time.Now}
}

func (l *Ledger) CreateAccount(ctx context.Context, a NewAccount) (Account, error) {
	return l.createAccount(ctx, l.q, a)
}

// CreateAccountTx creates an account inside the caller's transaction.
func (l *Ledger) CreateAccountTx(ctx context.Context, tx pgx.Tx, a NewAccount) (Account, error) {
	return l.createAccount(ctx, l.q.WithTx(tx), a)
}

func (l *Ledger) createAccount(ctx context.Context, q *store.Queries, a NewAccount) (Account, error) {
	if err := validateNewAccount(a); err != nil {
		return Account{}, err
	}
	row, err := q.CreateAccount(ctx, store.CreateAccountParams{
		Code:          a.Code,
		Name:          a.Name,
		Type:          string(a.Type),
		Currency:      a.Currency,
		AllowNegative: a.AllowNegative,
		Metadata:      orEmptyObject(a.Metadata),
	})
	if err != nil {
		return Account{}, MapError(err)
	}
	return accountFromRow(row), nil
}

func (l *Ledger) GetAccount(ctx context.Context, id uuid.UUID) (Account, error) {
	row, err := l.q.GetAccount(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, fmt.Errorf("%w: %s", ErrAccountNotFound, id)
	}
	if err != nil {
		return Account{}, err
	}
	return accountFromRow(row), nil
}

func (l *Ledger) GetAccountByCode(ctx context.Context, code string) (Account, error) {
	row, err := l.q.GetAccountByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, fmt.Errorf("%w: %s", ErrAccountNotFound, code)
	}
	if err != nil {
		return Account{}, err
	}
	return accountFromRow(row), nil
}

// Post atomically records a balanced set of postings. It is safe to retry
// with the same idempotency key.
func (l *Ledger) Post(ctx context.Context, r PostRequest) (Transaction, error) {
	var result Transaction
	err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) (err error) {
		result, err = l.PostTx(ctx, tx, r)
		return err
	})
	if err != nil {
		return Transaction{}, MapError(err)
	}
	return result, nil
}

// PostTx posts inside the caller's transaction, so ledger entries commit or
// roll back together with the caller's own writes. The balance invariant is
// checked when the caller commits; pass commit errors through MapError.
func (l *Ledger) PostTx(ctx context.Context, tx pgx.Tx, r PostRequest) (Transaction, error) {
	if err := validatePostRequest(r); err != nil {
		return Transaction{}, err
	}
	hash, err := requestHash(struct {
		Kind        string
		Description string
		EffectiveAt time.Time
		Metadata    json.RawMessage
		Postings    []PostingInput
	}{"post", r.Description, r.EffectiveAt.UTC(), orEmptyObject(r.Metadata), r.Postings})
	if err != nil {
		return Transaction{}, err
	}
	return l.postTx(ctx, tx, r, hash, uuid.NullUUID{})
}

// Reverse posts a new transaction that exactly mirrors the original with
// debits and credits swapped. The original is never modified, and a
// transaction can be reversed at most once.
func (l *Ledger) Reverse(ctx context.Context, r ReverseRequest) (Transaction, error) {
	var result Transaction
	err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) (err error) {
		result, err = l.ReverseTx(ctx, tx, r)
		return err
	})
	if err != nil {
		return Transaction{}, MapError(err)
	}
	return result, nil
}

// ReverseTx reverses inside the caller's transaction.
func (l *Ledger) ReverseTx(ctx context.Context, tx pgx.Tx, r ReverseRequest) (Transaction, error) {
	if r.IdempotencyKey == "" {
		return Transaction{}, fmt.Errorf("%w: idempotency key is required", ErrInvalidRequest)
	}
	original, err := l.getTransaction(ctx, l.q.WithTx(tx), r.TransactionID)
	if err != nil {
		return Transaction{}, err
	}
	description := r.Description
	if description == "" {
		description = "Reversal of " + original.ID.String()
	}

	postings := make([]PostingInput, len(original.Postings))
	for i, p := range original.Postings {
		postings[i] = PostingInput{
			AccountID: p.AccountID,
			Direction: p.Direction.Opposite(),
			Amount:    p.Amount,
			Currency:  p.Currency,
		}
	}

	hash, err := requestHash(struct {
		Kind          string
		TransactionID uuid.UUID
		Description   string
	}{"reverse", original.ID, description})
	if err != nil {
		return Transaction{}, err
	}
	return l.postTx(ctx, tx, PostRequest{
		IdempotencyKey: r.IdempotencyKey,
		Description:    description,
		Postings:       postings,
	}, hash, uuid.NullUUID{UUID: original.ID, Valid: true})
}

func (l *Ledger) postTx(ctx context.Context, tx pgx.Tx, r PostRequest, hash string, reverses uuid.NullUUID) (Transaction, error) {
	result, err := l.postInTx(ctx, l.q.WithTx(tx), r, hash, reverses)
	if err != nil {
		return Transaction{}, MapError(err)
	}
	return result, nil
}

func (l *Ledger) postInTx(ctx context.Context, q *store.Queries, r PostRequest, hash string, reverses uuid.NullUUID) (Transaction, error) {
	effectiveAt := r.EffectiveAt
	if effectiveAt.IsZero() {
		effectiveAt = l.now()
	}
	var result Transaction
	err := func() error {

		// The unique index on idempotency_key serialises concurrent requests
		// with the same key: the second insert waits for the first to commit,
		// then sees the conflict and falls through to the replay path.
		row, err := q.InsertTransaction(ctx, store.InsertTransactionParams{
			IdempotencyKey: r.IdempotencyKey,
			RequestHash:    hash,
			Description:    r.Description,
			ReversesID:     reverses,
			Metadata:       orEmptyObject(r.Metadata),
			EffectiveAt:    effectiveAt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			existing, err := q.GetTransactionByIdempotencyKey(ctx, r.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing.RequestHash != hash {
				return fmt.Errorf("%w: %q", ErrIdempotencyKeyReused, r.IdempotencyKey)
			}
			result, err = loadTransaction(ctx, q, existing)
			result.Replayed = true
			return err
		}
		if err != nil {
			return err
		}

		ids := uniqueAccountIDs(r.Postings)
		accountRows, err := q.GetAccountsByIDs(ctx, ids)
		if err != nil {
			return err
		}
		accounts := make(map[uuid.UUID]store.Account, len(accountRows))
		for _, a := range accountRows {
			accounts[a.ID] = a
		}
		for _, p := range r.Postings {
			a, ok := accounts[p.AccountID]
			if !ok {
				return fmt.Errorf("%w: %s", ErrAccountNotFound, p.AccountID)
			}
			if a.Currency != p.Currency {
				return fmt.Errorf("%w: account %s is %s, posting is %s", ErrCurrencyMismatch, a.Code, a.Currency, p.Currency)
			}
		}

		balances, err := q.LockBalances(ctx, ids)
		if err != nil {
			return err
		}
		if err := checkFunds(balances, accounts, r.Postings); err != nil {
			return err
		}

		ordered := increasesFirst(r.Postings, accounts)
		params := store.InsertPostingsParams{TransactionID: row.ID}
		for _, p := range ordered {
			params.AccountIds = append(params.AccountIds, p.AccountID)
			params.Currencies = append(params.Currencies, p.Currency)
			params.Directions = append(params.Directions, string(p.Direction))
			params.Amounts = append(params.Amounts, p.Amount)
		}
		if err := q.InsertPostings(ctx, params); err != nil {
			return err
		}

		result, err = loadTransaction(ctx, q, row)
		return err
	}()
	return result, err
}

// checkFunds rejects the request early, with a clear error, if any account
// that disallows negative balances would be overdrawn. The balance rows are
// already locked, so the result cannot be invalidated by a concurrent post.
// The database CHECK constraint is the final guard.
func checkFunds(balances []store.AccountBalance, accounts map[uuid.UUID]store.Account, postings []PostingInput) error {
	projected := make(map[uuid.UUID]int64, len(balances))
	for _, b := range balances {
		projected[b.AccountID] = b.Balance
	}
	for _, p := range postings {
		delta := p.Amount
		if string(p.Direction) != accounts[p.AccountID].NormalBalance {
			delta = -delta
		}
		next, ok := addChecked(projected[p.AccountID], delta)
		if !ok {
			return fmt.Errorf("%w: balance overflow on account %s", ErrInvalidRequest, accounts[p.AccountID].Code)
		}
		projected[p.AccountID] = next
	}
	for _, b := range balances {
		if !b.AllowNegative && projected[b.AccountID] < 0 {
			a := accounts[b.AccountID]
			return fmt.Errorf("%w: account %s has %d %s, needs %d more",
				ErrInsufficientFunds, a.Code, b.Balance, a.Currency, -projected[b.AccountID])
		}
	}
	return nil
}

// increasesFirst orders postings so that, per account, postings that raise
// the balance are applied before ones that lower it. The balance trigger
// checks the non-negative constraint after every row, so this keeps it
// consistent with checkFunds, which only looks at the net result.
func increasesFirst(postings []PostingInput, accounts map[uuid.UUID]store.Account) []PostingInput {
	ordered := append([]PostingInput(nil), postings...)
	sort.SliceStable(ordered, func(i, j int) bool {
		inc := func(p PostingInput) bool { return string(p.Direction) == accounts[p.AccountID].NormalBalance }
		return inc(ordered[i]) && !inc(ordered[j])
	})
	return ordered
}

func (l *Ledger) GetTransaction(ctx context.Context, id uuid.UUID) (Transaction, error) {
	return l.getTransaction(ctx, l.q, id)
}

func (l *Ledger) getTransaction(ctx context.Context, q *store.Queries, id uuid.UUID) (Transaction, error) {
	row, err := q.GetTransaction(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, fmt.Errorf("%w: %s", ErrTransactionNotFound, id)
	}
	if err != nil {
		return Transaction{}, err
	}
	return loadTransaction(ctx, q, row)
}

// Balance returns the current cached balance.
func (l *Ledger) Balance(ctx context.Context, accountID uuid.UUID) (Balance, error) {
	row, err := l.q.GetBalance(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Balance{}, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
	}
	if err != nil {
		return Balance{}, err
	}
	return Balance{
		AccountID:    row.AccountID,
		Currency:     row.Currency,
		DebitsTotal:  row.DebitsTotal,
		CreditsTotal: row.CreditsTotal,
		Balance:      row.Balance,
		Version:      row.Version,
		AsOf:         l.now(),
	}, nil
}

// BalanceAt recomputes the balance from postings effective at or before at.
func (l *Ledger) BalanceAt(ctx context.Context, accountID uuid.UUID, at time.Time) (Balance, error) {
	account, err := l.GetAccount(ctx, accountID)
	if err != nil {
		return Balance{}, err
	}
	totals, err := l.q.GetBalanceAt(ctx, store.GetBalanceAtParams{AccountID: accountID, At: at})
	if err != nil {
		return Balance{}, err
	}
	balance := totals.CreditsTotal - totals.DebitsTotal
	if account.NormalBalance == Debit {
		balance = -balance
	}
	return Balance{
		AccountID:    accountID,
		Currency:     account.Currency,
		DebitsTotal:  totals.DebitsTotal,
		CreditsTotal: totals.CreditsTotal,
		Balance:      balance,
		AsOf:         at,
	}, nil
}

// CheckIntegrity re-derives every cached balance from raw postings and
// computes the trial balance. A healthy ledger has no mismatches and equal
// debits and credits in every currency.
func (l *Ledger) CheckIntegrity(ctx context.Context) (IntegrityReport, error) {
	var report IntegrityReport
	totals, err := l.q.TrialBalance(ctx)
	if err != nil {
		return report, err
	}
	for _, t := range totals {
		report.TrialBalance = append(report.TrialBalance, CurrencyTotals{
			Currency:     t.Currency,
			TotalDebits:  t.TotalDebits,
			TotalCredits: t.TotalCredits,
		})
	}
	mismatches, err := l.q.ListBalanceMismatches(ctx)
	if err != nil {
		return report, err
	}
	for _, m := range mismatches {
		report.Mismatches = append(report.Mismatches, BalanceMismatch(m))
	}
	return report, nil
}

func loadTransaction(ctx context.Context, q *store.Queries, row store.LedgerTransaction) (Transaction, error) {
	postingRows, err := q.ListPostingsByTransaction(ctx, row.ID)
	if err != nil {
		return Transaction{}, err
	}
	t := Transaction{
		ID:             row.ID,
		IdempotencyKey: row.IdempotencyKey,
		Description:    row.Description,
		Metadata:       row.Metadata,
		EffectiveAt:    row.EffectiveAt,
		CreatedAt:      row.CreatedAt,
	}
	if row.ReversesID.Valid {
		id := row.ReversesID.UUID
		t.ReversesID = &id
	}
	for _, p := range postingRows {
		t.Postings = append(t.Postings, Posting{
			ID:            p.ID,
			TransactionID: p.TransactionID,
			AccountID:     p.AccountID,
			Direction:     Direction(p.Direction),
			Amount:        p.Amount,
			Currency:      p.Currency,
			EffectiveAt:   p.EffectiveAt,
		})
	}
	return t, nil
}

func accountFromRow(r store.Account) Account {
	return Account{
		ID:            r.ID,
		Code:          r.Code,
		Name:          r.Name,
		Type:          AccountType(r.Type),
		NormalBalance: Direction(r.NormalBalance),
		Currency:      r.Currency,
		AllowNegative: r.AllowNegative,
		Metadata:      r.Metadata,
		CreatedAt:     r.CreatedAt,
	}
}

func uniqueAccountIDs(postings []PostingInput) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(postings))
	var ids []uuid.UUID
	for _, p := range postings {
		if !seen[p.AccountID] {
			seen[p.AccountID] = true
			ids = append(ids, p.AccountID)
		}
	}
	return ids
}

func orEmptyObject(m json.RawMessage) json.RawMessage {
	if len(m) == 0 {
		return json.RawMessage(`{}`)
	}
	return m
}

func requestHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// MapError translates database errors into the ledger's sentinel errors.
func MapError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "FC001":
		return fmt.Errorf("%w: %s", ErrUnbalanced, pgErr.Message)
	case "FC002":
		return fmt.Errorf("%w: %s", ErrImmutable, pgErr.Message)
	case "23514":
		if pgErr.ConstraintName == "account_balances_non_negative" {
			return fmt.Errorf("%w: %s", ErrInsufficientFunds, pgErr.Message)
		}
	case "23505":
		switch pgErr.ConstraintName {
		case "accounts_code_key":
			return fmt.Errorf("%w: %s", ErrDuplicateAccountCode, pgErr.Detail)
		case "ledger_transactions_reverses_id_key":
			return ErrAlreadyReversed
		}
	case "23503":
		return fmt.Errorf("%w: %s", ErrAccountNotFound, pgErr.Detail)
	case "22P02", "23502":
		return fmt.Errorf("%w: %s", ErrInvalidRequest, pgErr.Message)
	}
	return err
}

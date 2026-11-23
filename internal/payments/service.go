package payments

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"fincore/internal/ledger"
	"fincore/internal/metrics"
	"fincore/internal/provider"
	"fincore/internal/store"
)

// Ledger account layout (per currency):
//
//	system:psp_clearing:<CUR>       asset      money held at the processor
//	system:fee_revenue:<CUR>        revenue    platform fees
//	system:payouts_in_transit:<CUR> liability  payouts sent, not yet confirmed
//	merchant:<id>:payable           liability  what the platform owes the merchant
//	system:bank:<CUR>               asset      the platform's bank account
//	system:processor_fees:<CUR>     expense    fees charged by the processor
type systemAccounts struct {
	Clearing      ledger.Account
	Fees          ledger.Account
	InTransit     ledger.Account
	Bank          ledger.Account
	ProcessorFees ledger.Account
}

// Ordering rule for every money-moving operation, inside one DB transaction:
//
//  1. lock the business row and check the state machine,
//  2. post to the ledger (enforces balances and funds),
//  3. call the provider,
//  4. update state and append the audit event, then commit.
//
// A local failure therefore never leaves money moved at the provider, and a
// provider failure rolls back the ledger. The remaining gap (provider
// succeeded, commit failed) is closed by reconciliation against the
// provider's settlement reports.
type Service struct {
	// Mode is "test" or "live"; it sets the API key prefix.
	Mode            string
	pool            *pgxpool.Pool
	q               *store.Queries
	ledger          *ledger.Ledger
	providers       map[string]provider.Provider
	defaultProvider string

	mu     sync.Mutex
	system map[string]systemAccounts
}

// NewService registers providers; the first is the default for new payments.
func NewService(pool *pgxpool.Pool, l *ledger.Ledger, providers ...provider.Provider) *Service {
	s := &Service{
		Mode:      "test",
		pool:      pool,
		q:         store.New(pool),
		ledger:    l,
		providers: map[string]provider.Provider{},
		system:    map[string]systemAccounts{},
	}
	for i, p := range providers {
		if i == 0 {
			s.defaultProvider = p.Name()
		}
		s.providers[p.Name()] = p
	}
	return s
}

func (s *Service) Provider(name string) (provider.Provider, bool) {
	p, ok := s.providers[name]
	return p, ok
}

// ---- merchants -------------------------------------------------------------

func (s *Service) CreateMerchant(ctx context.Context, r CreateMerchantRequest) (Merchant, error) {
	switch {
	case r.Name == "":
		return Merchant{}, fmt.Errorf("%w: name is required", ErrInvalidRequest)
	case !ledger.ValidCurrency(r.Currency):
		return Merchant{}, fmt.Errorf("%w: currency %q is invalid", ErrInvalidRequest, r.Currency)
	case r.FeeBps < 0 || r.FeeBps > 10000:
		return Merchant{}, fmt.Errorf("%w: fee_bps must be between 0 and 10000", ErrInvalidRequest)
	}
	if _, err := s.systemAccounts(ctx, r.Currency); err != nil {
		return Merchant{}, err
	}

	id := uuid.New()
	var m store.Merchant
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		account, err := s.ledger.CreateAccountTx(ctx, tx, ledger.NewAccount{
			Code:     "merchant:" + id.String() + ":payable",
			Name:     r.Name + " payable",
			Type:     ledger.Liability,
			Currency: r.Currency,
		})
		if err != nil {
			return err
		}
		m, err = s.q.WithTx(tx).CreateMerchant(ctx, store.CreateMerchantParams{
			ID: id, Name: r.Name, Currency: r.Currency, FeeBps: r.FeeBps, PayableAccountID: account.ID,
		})
		return err
	})
	if err != nil {
		return Merchant{}, mapError(err)
	}
	return merchantFromRow(m), nil
}

func (s *Service) GetMerchant(ctx context.Context, id uuid.UUID) (Merchant, error) {
	m, err := s.getMerchant(ctx, s.q, id)
	return merchantFromRow(m), err
}

func (s *Service) MerchantBalance(ctx context.Context, id uuid.UUID) (MerchantBalance, error) {
	m, err := s.getMerchant(ctx, s.q, id)
	if err != nil {
		return MerchantBalance{}, err
	}
	b, err := s.ledger.Balance(ctx, m.PayableAccountID)
	if err != nil {
		return MerchantBalance{}, err
	}
	pending, err := s.q.SumPendingPayouts(ctx, id)
	if err != nil {
		return MerchantBalance{}, err
	}
	return MerchantBalance{MerchantID: id, Currency: m.Currency, Available: b.Balance, PendingPayouts: pending}, nil
}

// ---- shared internals --------------------------------------------------------

type transitionOpts struct {
	amount    *int64
	ledgerTxn uuid.UUID
	note      string
}

// transition applies a state change to a locked payment row and appends it to
// the audit trail. Callers must hold the row lock. A same-state "transition"
// (to == current) is allowed only when the state machine permits it, except
// for created, where it records a note such as a failed checkout attempt.
func (s *Service) transition(ctx context.Context, q *store.Queries, p store.Payment, to Status, o transitionOpts, mutate func(*store.UpdatePaymentParams)) (store.Payment, error) {
	same := Status(p.Status) == to && to == StatusCreated
	if !same && !CanTransition(Status(p.Status), to) {
		return store.Payment{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, p.Status, to)
	}
	u := store.UpdatePaymentParams{
		ID: p.ID, Status: string(to), ProviderRef: p.ProviderRef, ProviderPaymentID: p.ProviderPaymentID,
		NextAction: p.NextAction, AmountCaptured: p.AmountCaptured, AmountRefunded: p.AmountRefunded,
		FeeAmount: p.FeeAmount, FailureReason: p.FailureReason, CaptureTxnID: p.CaptureTxnID,
	}
	if mutate != nil {
		mutate(&u)
	}
	updated, err := q.UpdatePayment(ctx, u)
	if err != nil {
		return store.Payment{}, err
	}
	if !same {
		metrics.PaymentTransitions.WithLabelValues(updated.Provider, string(to)).Inc()
		if err := emit(ctx, q, updated.MerchantID, "payment", updated.ID, "payment."+string(to), paymentFromRow(updated)); err != nil {
			return store.Payment{}, err
		}
	}
	from := p.Status
	ledgerTxn := uuid.NullUUID{UUID: o.ledgerTxn, Valid: o.ledgerTxn != uuid.Nil}
	err = q.InsertPaymentEvent(ctx, store.InsertPaymentEventParams{
		PaymentID: p.ID, FromStatus: &from, ToStatus: string(to), Amount: o.amount, LedgerTxnID: ledgerTxn, Note: o.note,
	})
	return updated, err
}

// emit writes an event to the outbox inside the caller's transaction, so it
// is published if and only if the state change commits.
func emit(ctx context.Context, q *store.Queries, merchantID uuid.UUID, aggregate string, aggregateID uuid.UUID, eventType string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return q.InsertOutboxEvent(ctx, store.InsertOutboxEventParams{
		MerchantID: merchantID, AggregateType: aggregate, AggregateID: aggregateID, EventType: eventType, Payload: payload,
	})
}

// SetMerchantWebhook sets where the merchant receives events and returns
// the signing secret. Calling it again rotates the secret.
func (s *Service) SetMerchantWebhook(ctx context.Context, merchantID uuid.UUID, rawURL string) (WebhookEndpoint, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return WebhookEndpoint{}, fmt.Errorf("%w: webhook url must be an absolute http(s) URL", ErrInvalidRequest)
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return WebhookEndpoint{}, err
	}
	whsec := "whsec_" + hex.EncodeToString(secret)
	m, err := s.q.SetMerchantWebhook(ctx, store.SetMerchantWebhookParams{ID: merchantID, WebhookUrl: &rawURL, WebhookSecret: &whsec})
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookEndpoint{}, fmt.Errorf("%w: merchant %s", ErrNotFound, merchantID)
	}
	if err != nil {
		return WebhookEndpoint{}, err
	}
	return WebhookEndpoint{URL: *m.WebhookUrl, Secret: whsec}, nil
}

// pgxBegin runs fn in a transaction with transaction-bound queries.
func pgxBegin(ctx context.Context, s *Service, fn func(q *store.Queries) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return fn(s.q.WithTx(tx)) })
}

func (s *Service) lockPayment(ctx context.Context, q *store.Queries, id uuid.UUID) (store.Payment, error) {
	p, err := q.GetPaymentForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Payment{}, fmt.Errorf("%w: payment %s", ErrNotFound, id)
	}
	return p, err
}

func (s *Service) getMerchant(ctx context.Context, q *store.Queries, id uuid.UUID) (store.Merchant, error) {
	m, err := q.GetMerchant(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Merchant{}, fmt.Errorf("%w: merchant %s", ErrNotFound, id)
	}
	return m, err
}

func (s *Service) providerFor(name string) (provider.Provider, error) {
	p, ok := s.providers[name]
	if !ok {
		return nil, fmt.Errorf("%w: unknown provider %q", ErrInvalidRequest, name)
	}
	return p, nil
}

// systemAccounts returns (creating on first use) the platform's ledger
// accounts for a currency.
func (s *Service) systemAccounts(ctx context.Context, currency string) (systemAccounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sys, ok := s.system[currency]; ok {
		return sys, nil
	}
	ensure := func(name string, typ ledger.AccountType, allowNegative bool) (ledger.Account, error) {
		code := "system:" + name + ":" + currency
		a, err := s.ledger.GetAccountByCode(ctx, code)
		if errors.Is(err, ledger.ErrAccountNotFound) {
			a, err = s.ledger.CreateAccount(ctx, ledger.NewAccount{
				Code: code, Name: name + " " + currency, Type: typ, Currency: currency, AllowNegative: allowNegative,
			})
			if errors.Is(err, ledger.ErrDuplicateAccountCode) { // another instance created it
				a, err = s.ledger.GetAccountByCode(ctx, code)
			}
		}
		return a, err
	}
	var sys systemAccounts
	var err error
	// Clearing may go negative: payouts and refunds can be confirmed before
	// the processor's settlement for the matching captures is recorded.
	if sys.Clearing, err = ensure("psp_clearing", ledger.Asset, true); err != nil {
		return sys, err
	}
	if sys.Fees, err = ensure("fee_revenue", ledger.Revenue, false); err != nil {
		return sys, err
	}
	if sys.InTransit, err = ensure("payouts_in_transit", ledger.Liability, false); err != nil {
		return sys, err
	}
	// The bank may dip below zero in our books when refunds settle before
	// the matching captures; a real overdraft shows up in bank statements.
	if sys.Bank, err = ensure("bank", ledger.Asset, true); err != nil {
		return sys, err
	}
	if sys.ProcessorFees, err = ensure("processor_fees", ledger.Expense, false); err != nil {
		return sys, err
	}
	s.system[currency] = sys
	return sys, nil
}

// Fee returns the platform fee in minor units, rounding half up.
func Fee(amount int64, bps int32) int64 {
	return (amount*int64(bps) + 5_000) / 10_000
}

// proRata returns round(part * x / total) without overflowing int64.
func proRata(part, x, total int64) int64 {
	if total == 0 {
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(part), big.NewInt(x))
	n.Add(n, big.NewInt(total/2))
	return n.Quo(n, big.NewInt(total)).Int64()
}

func posting(a ledger.Account, d ledger.Direction, amount int64) ledger.PostingInput {
	return ledger.PostingInput{AccountID: a.ID, Direction: d, Amount: amount, Currency: a.Currency}
}

func posting2(accountID uuid.UUID, currency string, d ledger.Direction, amount int64) ledger.PostingInput {
	return ledger.PostingInput{AccountID: accountID, Direction: d, Amount: amount, Currency: currency}
}

// nonZero drops zero-amount legs, e.g. the fee leg for a zero-fee merchant.
func nonZero(ps ...ledger.PostingInput) []ledger.PostingInput {
	out := ps[:0]
	for _, p := range ps {
		if p.Amount != 0 {
			out = append(out, p)
		}
	}
	return out
}

func requestHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// providerError maps provider failures onto service errors.
func providerError(err error) error {
	switch {
	case errors.Is(err, provider.ErrUnavailable):
		return fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	case errors.Is(err, provider.ErrRejected), errors.Is(err, provider.ErrInvalidSignature):
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return err
}

func mapError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "FC003" {
		return fmt.Errorf("%w: %s", ErrInvalidTransition, pgErr.Message)
	}
	return ledger.MapError(err)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullUUID(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	return &n.UUID
}

func merchantFromRow(m store.Merchant) Merchant {
	return Merchant{ID: m.ID, Name: m.Name, Currency: m.Currency, FeeBps: m.FeeBps, PayableAccountID: m.PayableAccountID, WebhookURL: m.WebhookUrl, CreatedAt: m.CreatedAt}
}

func paymentFromRow(p store.Payment) Payment {
	out := Payment{
		ID: p.ID, MerchantID: p.MerchantID, Amount: p.Amount, Currency: p.Currency, Status: Status(p.Status),
		CaptureMethod: p.CaptureMethod, PaymentMethod: p.PaymentMethod, Description: p.Description,
		Provider: p.Provider, ProviderRef: p.ProviderRef, ProviderPaymentID: p.ProviderPaymentID,
		AmountCaptured: p.AmountCaptured, AmountRefunded: p.AmountRefunded, FeeAmount: p.FeeAmount,
		FailureReason: p.FailureReason, CaptureTxnID: nullUUID(p.CaptureTxnID),
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if out.Status == StatusCreated {
		out.NextAction = p.NextAction
	}
	return out
}

func refundFromRow(r store.Refund) Refund {
	return Refund{
		ID: r.ID, PaymentID: r.PaymentID, Amount: r.Amount, Currency: r.Currency, Reason: r.Reason,
		Status: r.Status, ProviderRefundID: r.ProviderRefundID, FailureReason: r.FailureReason,
		LedgerTxnID: r.LedgerTxnID, ReversalTxnID: nullUUID(r.ReversalTxnID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func payoutFromRow(p store.Payout) Payout {
	return Payout{
		ID: p.ID, MerchantID: p.MerchantID, Amount: p.Amount, Currency: p.Currency, Status: p.Status,
		FailureReason: p.FailureReason, HoldTxnID: p.HoldTxnID, SettleTxnID: nullUUID(p.SettleTxnID),
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

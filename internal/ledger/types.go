// Package ledger implements a double-entry ledger on PostgreSQL.
//
// Money is always an int64 count of minor units (e.g. paise for INR).
// Balances are never written directly: they are derived from immutable
// postings, and the database rejects any transaction whose debits and
// credits do not balance per currency.
package ledger

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type AccountType string

const (
	Asset     AccountType = "asset"
	Liability AccountType = "liability"
	Equity    AccountType = "equity"
	Revenue   AccountType = "revenue"
	Expense   AccountType = "expense"
)

func (t AccountType) Valid() bool {
	switch t {
	case Asset, Liability, Equity, Revenue, Expense:
		return true
	}
	return false
}

// NormalBalance is the side that increases an account of this type.
func (t AccountType) NormalBalance() Direction {
	if t == Asset || t == Expense {
		return Debit
	}
	return Credit
}

type Direction string

const (
	Debit  Direction = "debit"
	Credit Direction = "credit"
)

func (d Direction) Valid() bool { return d == Debit || d == Credit }

func (d Direction) Opposite() Direction {
	if d == Debit {
		return Credit
	}
	return Debit
}

type Account struct {
	ID            uuid.UUID
	Code          string
	Name          string
	Type          AccountType
	NormalBalance Direction
	Currency      string
	AllowNegative bool
	Metadata      json.RawMessage
	CreatedAt     time.Time
}

type NewAccount struct {
	Code     string
	Name     string
	Type     AccountType
	Currency string
	// AllowNegative lets the balance go below zero. Use it only for system
	// accounts such as external clearing accounts; customer and merchant
	// accounts must never be overdrawn.
	AllowNegative bool
	Metadata      json.RawMessage
}

// Balance is expressed on the account's normal side: a positive balance on an
// asset account means debits exceed credits; on a liability, the reverse.
type Balance struct {
	AccountID    uuid.UUID
	Currency     string
	DebitsTotal  int64
	CreditsTotal int64
	Balance      int64
	Version      int64
	AsOf         time.Time
}

type PostingInput struct {
	AccountID uuid.UUID
	Direction Direction
	Amount    int64
	Currency  string
}

type PostRequest struct {
	// IdempotencyKey makes Post safe to retry: the same key with the same
	// request returns the original transaction instead of posting twice.
	IdempotencyKey string
	Description    string
	// EffectiveAt is when the money movement happened economically.
	// Defaults to now.
	EffectiveAt time.Time
	Metadata    json.RawMessage
	Postings    []PostingInput
}

type ReverseRequest struct {
	TransactionID  uuid.UUID
	IdempotencyKey string
	Description    string
}

type Posting struct {
	ID            int64     `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	Direction     Direction `json:"direction"`
	Amount        int64     `json:"amount"`
	Currency      string    `json:"currency"`
	EffectiveAt   time.Time `json:"effective_at"`
}

type Transaction struct {
	ID             uuid.UUID       `json:"id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Description    string          `json:"description"`
	ReversesID     *uuid.UUID      `json:"reverses_id"`
	Metadata       json.RawMessage `json:"metadata"`
	EffectiveAt    time.Time       `json:"effective_at"`
	CreatedAt      time.Time       `json:"created_at"`
	Postings       []Posting       `json:"postings"`
	// Replayed is true when this result came from an earlier request with the
	// same idempotency key, not from a new posting.
	Replayed bool `json:"-"`
}

type CurrencyTotals struct {
	Currency     string `json:"currency"`
	TotalDebits  int64  `json:"total_debits"`
	TotalCredits int64  `json:"total_credits"`
}

type BalanceMismatch struct {
	AccountID     uuid.UUID `json:"account_id"`
	CachedDebits  int64     `json:"cached_debits"`
	CachedCredits int64     `json:"cached_credits"`
	PostedDebits  int64     `json:"posted_debits"`
	PostedCredits int64     `json:"posted_credits"`
}

// IntegrityReport is the result of independently re-deriving the ledger's
// invariants from raw postings.
type IntegrityReport struct {
	TrialBalance []CurrencyTotals
	Mismatches   []BalanceMismatch
}

func (r IntegrityReport) OK() bool {
	if len(r.Mismatches) > 0 {
		return false
	}
	for _, t := range r.TrialBalance {
		if t.TotalDebits != t.TotalCredits {
			return false
		}
	}
	return true
}

var (
	ErrInvalidRequest       = errors.New("ledger: invalid request")
	ErrUnbalanced           = errors.New("ledger: debits and credits do not balance")
	ErrAccountNotFound      = errors.New("ledger: account not found")
	ErrTransactionNotFound  = errors.New("ledger: transaction not found")
	ErrCurrencyMismatch     = errors.New("ledger: posting currency does not match account currency")
	ErrInsufficientFunds    = errors.New("ledger: insufficient funds")
	ErrIdempotencyKeyReused = errors.New("ledger: idempotency key reused with a different request")
	ErrAlreadyReversed      = errors.New("ledger: transaction already reversed")
	ErrDuplicateAccountCode = errors.New("ledger: account code already exists")
	ErrImmutable            = errors.New("ledger: ledger records are immutable")
)

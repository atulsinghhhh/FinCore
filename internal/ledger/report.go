package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"fincore/internal/store"
)

type AccountBalance struct {
	ID           uuid.UUID   `json:"id"`
	Code         string      `json:"code"`
	Name         string      `json:"name"`
	Type         AccountType `json:"type"`
	Currency     string      `json:"currency"`
	DebitsTotal  int64       `json:"debits_total"`
	CreditsTotal int64       `json:"credits_total"`
	Balance      int64       `json:"balance"`
}

// AccountBalances lists accounts with their balances; codePrefix filters by
// account code (e.g. "system:") and may be empty.
func (l *Ledger) AccountBalances(ctx context.Context, codePrefix string) ([]AccountBalance, error) {
	var prefix *string
	if codePrefix != "" {
		prefix = &codePrefix
	}
	rows, err := l.q.ListAccountBalances(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]AccountBalance, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountBalance{
			ID: r.ID, Code: r.Code, Name: r.Name, Type: AccountType(r.Type), Currency: r.Currency,
			DebitsTotal: r.DebitsTotal, CreditsTotal: r.CreditsTotal, Balance: r.Balance,
		})
	}
	return out, nil
}

type StatementLine struct {
	PostingID     int64     `json:"posting_id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	Description   string    `json:"description"`
	Direction     Direction `json:"direction"`
	Amount        int64     `json:"amount"`
	EffectiveAt   time.Time `json:"effective_at"`
	// Balance is the running balance after this line.
	Balance int64 `json:"balance"`
}

type Statement struct {
	AccountID uuid.UUID       `json:"account_id"`
	Currency  string          `json:"currency"`
	From      time.Time       `json:"from"`
	To        time.Time       `json:"to"`
	Opening   int64           `json:"opening_balance"`
	Closing   int64           `json:"closing_balance"`
	Lines     []StatementLine `json:"lines"`
	// Truncated is true when the period has more than MaxStatementLines.
	Truncated bool `json:"truncated"`
}

const MaxStatementLines = 1000

// Statement returns the account's postings effective in [from, to) with a
// running balance, starting from the balance just before from.
func (l *Ledger) Statement(ctx context.Context, accountID uuid.UUID, from, to time.Time) (Statement, error) {
	if !to.After(from) {
		return Statement{}, fmt.Errorf("%w: statement period is empty", ErrInvalidRequest)
	}
	opening, err := l.BalanceAt(ctx, accountID, from.Add(-time.Microsecond))
	if err != nil {
		return Statement{}, err
	}
	account, err := l.GetAccount(ctx, accountID)
	if err != nil {
		return Statement{}, err
	}
	rows, err := l.q.ListAccountPostings(ctx, store.ListAccountPostingsParams{
		AccountID: accountID, FromAt: from, ToAt: to, Lim: MaxStatementLines + 1,
	})
	if err != nil {
		return Statement{}, err
	}
	st := Statement{AccountID: accountID, Currency: account.Currency, From: from, To: to, Opening: opening.Balance, Lines: []StatementLine{}}
	if len(rows) > MaxStatementLines {
		rows, st.Truncated = rows[:MaxStatementLines], true
	}
	running := opening.Balance
	for _, r := range rows {
		if Direction(r.Direction) == account.NormalBalance {
			running += r.Amount
		} else {
			running -= r.Amount
		}
		st.Lines = append(st.Lines, StatementLine{
			PostingID: r.ID, TransactionID: r.TransactionID, Description: r.Description,
			Direction: Direction(r.Direction), Amount: r.Amount, EffectiveAt: r.EffectiveAt, Balance: running,
		})
	}
	st.Closing = running
	return st, nil
}

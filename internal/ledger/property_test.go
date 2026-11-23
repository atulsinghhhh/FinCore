package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestLedgerInvariants runs random sequences of transfers and reversals
// against a simple in-memory model, then checks that:
//   - every balance matches the model,
//   - no account that disallows negatives ever went below zero,
//   - cached balances equal balances re-derived from postings,
//   - total debits equal total credits.
func TestLedgerInvariants(t *testing.T) {
	l := newTestLedger(t)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(2, 5).Draw(rt, "accounts")
		accounts := make([]Account, n)
		model := make([]int64, n)
		for i := range accounts {
			accounts[i] = newAccount(rt, l, Liability, rapid.Bool().Draw(rt, "allowNegative"))
		}

		type posted struct {
			txn      Transaction
			from, to int
			amount   int64
			reversed bool
		}
		var history []*posted

		steps := rapid.IntRange(1, 30).Draw(rt, "steps")
		for range steps {
			if len(history) > 0 && rapid.IntRange(0, 4).Draw(rt, "op") == 0 {
				p := history[rapid.IntRange(0, len(history)-1).Draw(rt, "reverse")]
				_, err := l.Reverse(ctx, ReverseRequest{TransactionID: p.txn.ID, IdempotencyKey: uuid.NewString()})
				switch {
				case p.reversed:
					require.ErrorIs(rt, err, ErrAlreadyReversed)
				case p.from != p.to && !accounts[p.to].AllowNegative && model[p.to] < p.amount:
					require.ErrorIs(rt, err, ErrInsufficientFunds)
				default:
					require.NoError(rt, err)
					model[p.from] += p.amount
					model[p.to] -= p.amount
					p.reversed = true
				}
				continue
			}

			from := rapid.IntRange(0, n-1).Draw(rt, "from")
			to := rapid.IntRange(0, n-1).Draw(rt, "to")
			amount := rapid.Int64Range(1, 10_000).Draw(rt, "amount")
			txn, err := l.Post(ctx, PostRequest{
				IdempotencyKey: uuid.NewString(),
				Description:    "random transfer",
				Postings:       transfer(accounts[from], accounts[to], amount),
			})

			// A self-transfer nets to zero, so it never overdraws.
			wouldOverdraw := from != to && !accounts[from].AllowNegative && model[from] < amount
			if wouldOverdraw {
				require.True(rt, errors.Is(err, ErrInsufficientFunds), "want insufficient funds, got %v", err)
				continue
			}
			require.NoError(rt, err)
			model[from] -= amount
			model[to] += amount
			history = append(history, &posted{txn: txn, from: from, to: to, amount: amount})
		}

		for i, a := range accounts {
			got := balanceOf(rt, l, a)
			require.Equal(rt, model[i], got, "account %d", i)
			if !a.AllowNegative {
				require.GreaterOrEqual(rt, got, int64(0))
			}
		}
		requireIntegrity(rt, l)
	})
}

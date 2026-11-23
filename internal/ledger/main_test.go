package ledger

import (
	"context"
	"flag"
	"log"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"fincore/internal/testutil"
)

// testPool is a pool to a throwaway Postgres started for this test binary.
// It is nil under -short, and integration tests skip themselves.
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

func newTestLedger(t require.TestingT) *Ledger {
	if testPool == nil {
		if tt, ok := t.(*testing.T); ok {
			tt.Skip("integration test: run without -short")
		}
	}
	return New(testPool)
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// newAccount creates an INR account with a unique code so tests never collide.
func newAccount(t require.TestingT, l *Ledger, typ AccountType, allowNegative bool) Account {
	a, err := l.CreateAccount(context.Background(), NewAccount{
		Code:          string(typ) + ":" + uuid.NewString(),
		Name:          "test " + string(typ),
		Type:          typ,
		Currency:      "INR",
		AllowNegative: allowNegative,
	})
	require.NoError(t, err)
	return a
}

func balanceOf(t require.TestingT, l *Ledger, a Account) int64 {
	b, err := l.Balance(context.Background(), a.ID)
	require.NoError(t, err)
	return b.Balance
}

// transfer moves amount between two accounts with the same normal balance
// (e.g. two liabilities): it lowers from and raises to.
func transfer(from, to Account, amount int64) []PostingInput {
	return []PostingInput{
		{AccountID: from.ID, Direction: from.Type.NormalBalance().Opposite(), Amount: amount, Currency: "INR"},
		{AccountID: to.ID, Direction: to.Type.NormalBalance(), Amount: amount, Currency: "INR"},
	}
}

func requireIntegrity(t require.TestingT, l *Ledger) {
	report, err := l.CheckIntegrity(context.Background())
	require.NoError(t, err)
	require.Empty(t, report.Mismatches, "cached balances drifted from postings")
	require.True(t, report.OK(), "trial balance does not balance: %+v", report.TrialBalance)
}

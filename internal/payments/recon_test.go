package payments

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func csvFile(lines ...string) []byte {
	return []byte("settlement_id,entity_type,entity_id,amount,fee,tax,currency,settled_at\n" + strings.Join(lines, "\n") + "\n")
}

func (f fixture) systemBalance(t *testing.T, name string) int64 {
	t.Helper()
	a, err := f.ledger.GetAccountByCode(context.Background(), "system:"+name+":INR")
	require.NoError(t, err)
	b, err := f.ledger.Balance(context.Background(), a.ID)
	require.NoError(t, err)
	return b.Balance
}

func TestReconciliation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	settledAt := time.Now().Add(4 * 24 * time.Hour).UTC().Format(time.RFC3339) // captures below are > 3 days older

	p1 := f.pay(t, 100000)
	p2 := f.pay(t, 50000)
	p3 := f.pay(t, 7000) // the processor never settles this one
	refund, err := f.svc.Refund(ctx, p1.ID, RefundRequest{IdempotencyKey: uuid.NewString(), Amount: ptr(int64(20000))})
	require.NoError(t, err)

	clearingBefore := f.systemBalance(t, "psp_clearing")
	bankBefore := f.systemBalance(t, "bank")
	file := csvFile(
		fmt.Sprintf("setl_1,payment,%s,100000,2000,360,INR,%s", *p1.ProviderPaymentID, settledAt),
		fmt.Sprintf("setl_1,payment,%s,49000,0,0,INR,%s", *p2.ProviderPaymentID, settledAt),
		fmt.Sprintf("setl_1,refund,%s,20000,0,0,INR,%s", *refund.ProviderRefundID, settledAt),
		"setl_1,payment,pay_we_never_saw,9900,0,0,INR,"+settledAt,
		"setl_1,adjustment,adj_1,500,0,0,INR,"+settledAt,
	)

	report, err := f.svc.Reconcile(ctx, "sandbox", "setl_1.csv", file)
	require.NoError(t, err)
	require.Equal(t, 5, report.Summary.Lines)
	require.Equal(t, 2, report.Summary.Matched)
	require.Equal(t, 3, report.Summary.Exceptions)
	require.Equal(t, map[string]int{ReconMatched: 2, ReconAmountMismatch: 1, ReconMissingInLedger: 1, ReconUnsupported: 1}, report.Summary.ByResult)
	require.EqualValues(t, 2360, report.Summary.Fees)
	require.EqualValues(t, 100000-2360-20000, report.Summary.NetToBank)

	results := map[string]string{}
	for _, l := range report.Lines {
		results[l.EntityID] = l.Result
	}
	require.Equal(t, ReconAmountMismatch, results[*p2.ProviderPaymentID])

	var unsettled []uuid.UUID
	for _, u := range report.Summary.Unsettled {
		unsettled = append(unsettled, u.PaymentID)
	}
	require.Contains(t, unsettled, p3.ID)
	require.Contains(t, unsettled, p2.ID, "a mismatched line does not count as settled")
	require.NotContains(t, unsettled, p1.ID)

	// Ledger: matched money moved from clearing to the bank, net of processor fees.
	require.EqualValues(t, clearingBefore-100000+20000, f.systemBalance(t, "psp_clearing"))
	require.EqualValues(t, bankBefore+100000-2360-20000, f.systemBalance(t, "bank"))
	f.requireLedgerHealthy(t)

	// Importing the same file again is a no-op.
	again, err := f.svc.Reconcile(ctx, "sandbox", "setl_1.csv", file)
	require.NoError(t, err)
	require.True(t, again.Replayed)
	require.Equal(t, report.ID, again.ID)
	require.EqualValues(t, bankBefore+100000-2360-20000, f.systemBalance(t, "bank"))

	// A different file that repeats a settled payment flags it, without posting twice.
	dup, err := f.svc.Reconcile(ctx, "sandbox", "setl_2.csv", csvFile(
		fmt.Sprintf("setl_2,payment,%s,100000,2000,360,INR,%s", *p1.ProviderPaymentID, settledAt),
		fmt.Sprintf("setl_2,payment,%s,7000,140,25,INR,%s", *p3.ProviderPaymentID, settledAt),
	))
	require.NoError(t, err)
	require.Equal(t, map[string]int{ReconDuplicate: 1, ReconMatched: 1}, dup.Summary.ByResult)
	require.EqualValues(t, bankBefore+100000-2360-20000+7000-165, f.systemBalance(t, "bank"))
	f.requireLedgerHealthy(t)

	listed, err := f.svc.ListSettlementReports(ctx, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(listed), 2)
}

func TestReconciliationRejectsBadFiles(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for name, data := range map[string]string{
		"empty":            "",
		"missing column":   "settlement_id,entity_type,entity_id,amount,currency,settled_at\nx,payment,p,1,INR,2026-01-01\n",
		"negative amount":  string(csvFile("s,payment,p,-5,0,0,INR,2026-01-01")),
		"decimal amount":   string(csvFile("s,payment,p,10.50,0,0,INR,2026-01-01")),
		"bad date":         string(csvFile("s,payment,p,100,0,0,INR,yesterday")),
		"no lines":         string(csvFile())[:len("settlement_id,entity_type,entity_id,amount,fee,tax,currency,settled_at\n")],
		"unknown currency": string(csvFile("s,payment,p,100,0,0,rupees,2026-01-01")),
	} {
		_, err := f.svc.Reconcile(ctx, "sandbox", name, []byte(data))
		require.ErrorIs(t, err, ErrInvalidRequest, name)
	}
	_, err := f.svc.Reconcile(ctx, "stripe", "x.csv", csvFile("s,payment,p,100,0,0,INR,2026-01-01"))
	require.ErrorIs(t, err, ErrInvalidRequest, "unknown provider")
}

func TestMerchantStatement(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	start := time.Now().Add(-time.Second)
	p := f.pay(t, 100000)
	_, err := f.svc.Refund(ctx, p.ID, RefundRequest{IdempotencyKey: uuid.NewString(), Amount: ptr(int64(10000))})
	require.NoError(t, err)
	_, err = f.svc.CreatePayout(ctx, CreatePayoutRequest{MerchantID: f.merchant.ID, IdempotencyKey: uuid.NewString(), Amount: 50000})
	require.NoError(t, err)

	st, err := f.ledger.Statement(ctx, f.merchant.PayableAccountID, start, time.Now().Add(time.Second))
	require.NoError(t, err)
	require.Zero(t, st.Opening)
	require.Len(t, st.Lines, 3)
	require.EqualValues(t, []int64{98000, 98000 - 9800, 98000 - 9800 - 50000}, []int64{st.Lines[0].Balance, st.Lines[1].Balance, st.Lines[2].Balance})
	require.Equal(t, f.balance(t).Available, st.Closing)

	later, err := f.ledger.Statement(ctx, f.merchant.PayableAccountID, time.Now().Add(time.Second), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, st.Closing, later.Opening, "next period opens where the last closed")
	require.Empty(t, later.Lines)
}

// Command demo runs the ₹1,000 payment example against the local database
// and prints the resulting balances and integrity report.
//
//	make demo
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fincore/internal/ledger"
)

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	l := ledger.New(pool)

	run := time.Now().Format("20060102-150405")
	mk := func(code, name string, typ ledger.AccountType, allowNegative bool) ledger.Account {
		a, err := l.CreateAccount(ctx, ledger.NewAccount{
			Code: fmt.Sprintf("demo:%s:%s", run, code), Name: name, Type: typ, Currency: "INR", AllowNegative: allowNegative,
		})
		if err != nil {
			log.Fatal(err)
		}
		return a
	}
	psp := mk("psp_clearing", "Razorpay clearing", ledger.Asset, true)
	bank := mk("bank", "HDFC current account", ledger.Asset, false)
	merchant := mk("merchant_payable", "Merchant payable", ledger.Liability, false)
	fees := mk("fee_revenue", "Platform fee revenue", ledger.Revenue, false)

	post := func(desc string, postings ...ledger.PostingInput) {
		txn, err := l.Post(ctx, ledger.PostRequest{IdempotencyKey: uuid.NewString(), Description: desc, Postings: postings})
		if err != nil {
			log.Fatalf("%s: %v", desc, err)
		}
		fmt.Printf("✔ %-32s txn %s\n", desc, txn.ID)
	}
	p := func(a ledger.Account, d ledger.Direction, paise int64) ledger.PostingInput {
		return ledger.PostingInput{AccountID: a.ID, Direction: d, Amount: paise, Currency: "INR"}
	}

	post("Customer pays ₹1,000 (2% fee)", p(psp, ledger.Debit, 100000), p(merchant, ledger.Credit, 98000), p(fees, ledger.Credit, 2000))
	post("Razorpay settles to bank", p(bank, ledger.Debit, 100000), p(psp, ledger.Credit, 100000))
	post("Merchant payout ₹980", p(merchant, ledger.Debit, 98000), p(bank, ledger.Credit, 98000))

	_, err = l.Post(ctx, ledger.PostRequest{
		IdempotencyKey: uuid.NewString(), Description: "Second payout (should fail)",
		Postings: []ledger.PostingInput{p(merchant, ledger.Debit, 98000), p(bank, ledger.Credit, 98000)},
	})
	if errors.Is(err, ledger.ErrInsufficientFunds) {
		fmt.Printf("✘ %-32s rejected: %v\n", "Second payout ₹980", err)
	} else {
		log.Fatalf("expected insufficient funds, got %v", err)
	}

	fmt.Println("\nBalances")
	for _, a := range []ledger.Account{psp, bank, merchant, fees} {
		b, err := l.Balance(ctx, a.ID)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  %-24s %-9s ₹%10.2f\n", a.Name, a.Type, float64(b.Balance)/100) // display only
	}

	report, err := l.CheckIntegrity(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nIntegrity (whole ledger)")
	for _, t := range report.TrialBalance {
		fmt.Printf("  %s debits %d = credits %d\n", t.Currency, t.TotalDebits, t.TotalCredits)
	}
	fmt.Printf("  cached balance mismatches: %d\n  ok: %v\n", len(report.Mismatches), report.OK())
}

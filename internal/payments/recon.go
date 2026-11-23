package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fincore/internal/ledger"
	"fincore/internal/store"
)

// Settlement file format (CSV, header required, column order free):
//
//	settlement_id,entity_type,entity_id,amount,fee,tax,currency,settled_at
//	setl_123,payment,pay_ABC,100000,2000,360,INR,2026-11-24T10:00:00Z
//	setl_123,refund,rfnd_XYZ,20000,0,0,INR,2026-11-24
//
// amount, fee and tax are minor units. entity_id is the provider's payment
// or refund ID. Razorpay's settlement recon report maps onto this directly.
var settlementColumns = []string{"settlement_id", "entity_type", "entity_id", "amount", "fee", "tax", "currency", "settled_at"}

const (
	ReconMatched         = "matched"
	ReconMissingInLedger = "missing_in_ledger"
	ReconAmountMismatch  = "amount_mismatch"
	ReconStatusMismatch  = "status_mismatch"
	ReconDuplicate       = "duplicate"
	ReconUnsupported     = "unsupported"

	// SettlementGrace is how long after capture a payment may stay
	// unsettled before the report flags it.
	SettlementGrace = 3 * 24 * time.Hour
)

type SettlementLine struct {
	LineNo       int32      `json:"line_no"`
	SettlementID string     `json:"settlement_id"`
	EntityType   string     `json:"entity_type"`
	EntityID     string     `json:"entity_id"`
	Amount       int64      `json:"amount"`
	Fee          int64      `json:"fee"`
	Tax          int64      `json:"tax"`
	Currency     string     `json:"currency"`
	SettledAt    time.Time  `json:"settled_at"`
	Result       string     `json:"result"`
	Detail       string     `json:"detail"`
	PaymentID    *uuid.UUID `json:"payment_id"`
	RefundID     *uuid.UUID `json:"refund_id"`
	LedgerTxnID  *uuid.UUID `json:"ledger_txn_id"`
}

type UnsettledCapture struct {
	PaymentID         uuid.UUID `json:"payment_id"`
	ProviderPaymentID string    `json:"provider_payment_id"`
	Amount            int64     `json:"amount"`
	CapturedAt        time.Time `json:"captured_at"`
}

type ReconSummary struct {
	Lines      int            `json:"lines"`
	Matched    int            `json:"matched"`
	Exceptions int            `json:"exceptions"`
	ByResult   map[string]int `json:"by_result"`
	// Money for matched lines, in minor units.
	GrossSettled int64 `json:"gross_settled"`
	Refunded     int64 `json:"refunded"`
	Fees         int64 `json:"fees"`
	NetToBank    int64 `json:"net_to_bank"`
	// Unsettled lists captures older than SettlementGrace (relative to the
	// newest line in the file) that no settlement has matched yet.
	Unsettled []UnsettledCapture `json:"unsettled"`
}

type SettlementReport struct {
	ID        uuid.UUID        `json:"id"`
	Provider  string           `json:"provider"`
	Source    string           `json:"source"`
	SHA256    string           `json:"sha256"`
	CreatedAt time.Time        `json:"created_at"`
	Summary   ReconSummary     `json:"summary"`
	Lines     []SettlementLine `json:"lines"`
	Replayed  bool             `json:"-"`
}

type parsedLine struct {
	settlementID, entityType, entityID, currency string
	amount, fee, tax                             int64
	settledAt                                    time.Time
}

// Reconcile imports a settlement file and matches every line. The whole
// file is processed in one transaction; importing the same file twice
// returns the first result.
//
// Matched payment:  Dr bank (amount - fee - tax), Dr processor_fees (fee + tax), Cr psp_clearing (amount)
// Matched refund:   Dr psp_clearing (amount), Cr bank (amount)
func (s *Service) Reconcile(ctx context.Context, providerName, source string, data []byte) (SettlementReport, error) {
	if _, err := s.providerFor(providerName); err != nil {
		return SettlementReport{}, err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if existing, err := s.q.GetSettlementBatchByHash(ctx, store.GetSettlementBatchByHashParams{Provider: providerName, FileSha256: hash}); err == nil {
		report, err := s.GetSettlementReport(ctx, existing.ID)
		report.Replayed = true
		return report, err
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return SettlementReport{}, err
	}

	lines, err := parseSettlement(data)
	if err != nil {
		return SettlementReport{}, err
	}

	var batchID uuid.UUID
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		batch, err := q.InsertSettlementBatch(ctx, store.InsertSettlementBatchParams{
			Provider: providerName, Source: source, FileSha256: hash, LineCount: int32(len(lines)),
		})
		if err != nil {
			return err
		}
		batchID = batch.ID
		summary := ReconSummary{ByResult: map[string]int{}, Unsettled: []UnsettledCapture{}}
		var newest time.Time

		for i, l := range lines {
			row, err := s.matchLine(ctx, tx, q, providerName, l)
			if err != nil {
				return fmt.Errorf("line %d: %w", i+2, err)
			}
			row.BatchID, row.Provider, row.LineNo = batch.ID, providerName, int32(i+2)
			if _, err := q.InsertSettlementLine(ctx, row); err != nil {
				return fmt.Errorf("line %d: %w", i+2, err)
			}
			summary.Lines++
			summary.ByResult[row.Result]++
			if l.settledAt.After(newest) {
				newest = l.settledAt
			}
			if row.Result != ReconMatched {
				summary.Exceptions++
				continue
			}
			summary.Matched++
			summary.Fees += l.fee + l.tax
			if l.entityType == "payment" {
				summary.GrossSettled += l.amount
				summary.NetToBank += l.amount - l.fee - l.tax
			} else {
				summary.Refunded += l.amount
				summary.NetToBank -= l.amount + l.fee + l.tax
			}
		}

		unsettled, err := q.ListUnsettledCaptures(ctx, store.ListUnsettledCapturesParams{
			Provider: providerName, EffectiveAt: newest.Add(-SettlementGrace),
		})
		if err != nil {
			return err
		}
		for _, u := range unsettled {
			summary.Unsettled = append(summary.Unsettled, UnsettledCapture{
				PaymentID: u.ID, ProviderPaymentID: deref(u.ProviderPaymentID), Amount: u.AmountCaptured, CapturedAt: u.CapturedAt,
			})
		}
		raw, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		_, err = q.SetSettlementBatchSummary(ctx, store.SetSettlementBatchSummaryParams{ID: batch.ID, Summary: raw})
		return err
	})
	if err != nil {
		return SettlementReport{}, mapError(err)
	}
	return s.GetSettlementReport(ctx, batchID)
}

func (s *Service) matchLine(ctx context.Context, tx pgx.Tx, q *store.Queries, providerName string, l parsedLine) (store.InsertSettlementLineParams, error) {
	row := store.InsertSettlementLineParams{
		SettlementID: l.settlementID, EntityType: l.entityType, EntityID: l.entityID, Amount: l.amount,
		Fee: l.fee, Tax: l.tax, Currency: l.currency, SettledAt: l.settledAt,
	}
	result := func(r, detail string) (store.InsertSettlementLineParams, error) {
		row.Result, row.Detail = r, detail
		return row, nil
	}

	if l.entityType != "payment" && l.entityType != "refund" {
		return result(ReconUnsupported, "entity type "+l.entityType+" needs manual review")
	}
	settled, err := q.IsEntitySettled(ctx, store.IsEntitySettledParams{Provider: providerName, EntityType: l.entityType, EntityID: l.entityID})
	if err != nil {
		return row, err
	}
	if settled {
		return result(ReconDuplicate, "already settled in an earlier line or file")
	}
	sys, err := s.systemAccounts(ctx, l.currency)
	if err != nil {
		return row, err
	}

	var postings []ledger.PostingInput
	if l.entityType == "payment" {
		p, err := q.GetPaymentByProviderPaymentID(ctx, store.GetPaymentByProviderPaymentIDParams{Provider: providerName, ProviderPaymentID: &l.entityID})
		if errors.Is(err, pgx.ErrNoRows) {
			// The processor has money we never recorded: the "provider
			// succeeded, our commit failed" gap, or a payment completed
			// after we expired it.
			return result(ReconMissingInLedger, "no captured payment with this provider ID")
		}
		if err != nil {
			return row, err
		}
		row.PaymentID = uuid.NullUUID{UUID: p.ID, Valid: true}
		switch {
		case p.CaptureTxnID.Valid == false:
			return result(ReconStatusMismatch, "payment is "+p.Status+", not captured")
		case p.Currency != l.currency:
			return result(ReconAmountMismatch, "currency "+p.Currency+" vs "+l.currency)
		case p.AmountCaptured != l.amount:
			return result(ReconAmountMismatch, fmt.Sprintf("captured %d, settled %d", p.AmountCaptured, l.amount))
		case l.fee+l.tax > l.amount:
			return result(ReconAmountMismatch, "processor fees exceed the amount")
		}
		postings = nonZero(
			posting(sys.Bank, ledger.Debit, l.amount-l.fee-l.tax),
			posting(sys.ProcessorFees, ledger.Debit, l.fee+l.tax),
			posting(sys.Clearing, ledger.Credit, l.amount),
		)
	} else {
		r, err := q.GetRefundByProviderRef(ctx, &l.entityID)
		if errors.Is(err, pgx.ErrNoRows) {
			return result(ReconMissingInLedger, "no refund with this provider ID")
		}
		if err != nil {
			return row, err
		}
		row.RefundID = uuid.NullUUID{UUID: r.ID, Valid: true}
		row.PaymentID = uuid.NullUUID{UUID: r.PaymentID, Valid: true}
		switch {
		case r.Status == RefundFailed:
			return result(ReconStatusMismatch, "refund failed in our records but settled by the processor")
		case r.Amount != l.amount || r.Currency != l.currency:
			return result(ReconAmountMismatch, fmt.Sprintf("refunded %d %s, settled %d %s", r.Amount, r.Currency, l.amount, l.currency))
		}
		postings = nonZero(
			posting(sys.Clearing, ledger.Debit, l.amount),
			posting(sys.ProcessorFees, ledger.Debit, l.fee+l.tax),
			posting(sys.Bank, ledger.Credit, l.amount+l.fee+l.tax),
		)
	}

	txn, err := s.ledger.PostTx(ctx, tx, ledger.PostRequest{
		IdempotencyKey: "settlement:" + providerName + ":" + l.entityType + ":" + l.entityID,
		Description:    fmt.Sprintf("Settlement %s: %s %s", l.settlementID, l.entityType, l.entityID),
		EffectiveAt:    l.settledAt,
		Postings:       postings,
	})
	if err != nil {
		return row, err
	}
	row.LedgerTxnID = uuid.NullUUID{UUID: txn.ID, Valid: true}
	return result(ReconMatched, "")
}

func (s *Service) GetSettlementReport(ctx context.Context, id uuid.UUID) (SettlementReport, error) {
	b, err := s.q.GetSettlementBatch(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return SettlementReport{}, fmt.Errorf("%w: settlement batch %s", ErrNotFound, id)
	}
	if err != nil {
		return SettlementReport{}, err
	}
	report := SettlementReport{ID: b.ID, Provider: b.Provider, Source: b.Source, SHA256: b.FileSha256, CreatedAt: b.CreatedAt, Lines: []SettlementLine{}}
	if err := json.Unmarshal(b.Summary, &report.Summary); err != nil {
		return report, err
	}
	rows, err := s.q.ListSettlementLines(ctx, id)
	if err != nil {
		return report, err
	}
	for _, r := range rows {
		report.Lines = append(report.Lines, SettlementLine{
			LineNo: r.LineNo, SettlementID: r.SettlementID, EntityType: r.EntityType, EntityID: r.EntityID,
			Amount: r.Amount, Fee: r.Fee, Tax: r.Tax, Currency: r.Currency, SettledAt: r.SettledAt,
			Result: r.Result, Detail: r.Detail, PaymentID: nullUUID(r.PaymentID), RefundID: nullUUID(r.RefundID),
			LedgerTxnID: nullUUID(r.LedgerTxnID),
		})
	}
	return report, nil
}

func (s *Service) ListSettlementReports(ctx context.Context, limit int32) ([]SettlementReport, error) {
	rows, err := s.q.ListSettlementBatches(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SettlementReport, 0, len(rows))
	for _, b := range rows {
		r := SettlementReport{ID: b.ID, Provider: b.Provider, Source: b.Source, SHA256: b.FileSha256, CreatedAt: b.CreatedAt}
		_ = json.Unmarshal(b.Summary, &r.Summary)
		out = append(out, r)
	}
	return out, nil
}

func parseSettlement(data []byte) ([]parsedLine, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: settlement file has no header", ErrInvalidRequest)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, c := range settlementColumns {
		if _, ok := col[c]; !ok {
			return nil, fmt.Errorf("%w: settlement file is missing column %q", ErrInvalidRequest, c)
		}
	}

	var lines []parsedLine
	for n := 2; ; n++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %v", ErrInvalidRequest, n, err)
		}
		get := func(c string) string { return strings.TrimSpace(rec[col[c]]) }
		num := func(c string) (int64, error) {
			v := get(c)
			if v == "" {
				return 0, nil
			}
			x, err := strconv.ParseInt(v, 10, 64)
			if err != nil || x < 0 {
				return 0, fmt.Errorf("%w: line %d: %s must be a non-negative integer in minor units", ErrInvalidRequest, n, c)
			}
			return x, nil
		}
		l := parsedLine{
			settlementID: get("settlement_id"), entityType: strings.ToLower(get("entity_type")),
			entityID: get("entity_id"), currency: strings.ToUpper(get("currency")),
		}
		if l.amount, err = num("amount"); err != nil {
			return nil, err
		}
		if l.fee, err = num("fee"); err != nil {
			return nil, err
		}
		if l.tax, err = num("tax"); err != nil {
			return nil, err
		}
		if l.settledAt, err = parseTime(get("settled_at")); err != nil {
			return nil, fmt.Errorf("%w: line %d: settled_at: %v", ErrInvalidRequest, n, err)
		}
		if l.entityID == "" || l.settlementID == "" || !ledger.ValidCurrency(l.currency) || l.amount == 0 {
			return nil, fmt.Errorf("%w: line %d: settlement_id, entity_id, currency and a positive amount are required", ErrInvalidRequest, n)
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: settlement file has no lines", ErrInvalidRequest)
	}
	return lines, nil
}

func parseTime(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised time %q", v)
}

package payments

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fincore/internal/ledger"
	"fincore/internal/store"
)

// CreatePayout moves money from the merchant's available balance into
// transit. The bank later confirms (CompletePayout) or rejects (FailPayout).
//
//	Dr merchant payable    amount
//	  Cr payouts_in_transit  amount
func (s *Service) CreatePayout(ctx context.Context, r CreatePayoutRequest) (Payout, error) {
	switch {
	case r.IdempotencyKey == "":
		return Payout{}, fmt.Errorf("%w: idempotency key is required", ErrInvalidRequest)
	case r.Amount <= 0 || r.Amount > MaxAmount:
		return Payout{}, fmt.Errorf("%w: amount must be between 1 and %d", ErrInvalidRequest, MaxAmount)
	}
	m, err := s.getMerchant(ctx, s.q, r.MerchantID)
	if err != nil {
		return Payout{}, err
	}
	sys, err := s.systemAccounts(ctx, m.Currency)
	if err != nil {
		return Payout{}, err
	}
	hash, err := requestHash(r)
	if err != nil {
		return Payout{}, err
	}
	if existing, err := s.replayPayout(ctx, r.MerchantID, r.IdempotencyKey, hash); !errors.Is(err, ErrNotFound) {
		return existing, err
	}

	var payout store.Payout
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		id := uuid.New()
		txn, err := s.ledger.PostTx(ctx, tx, ledger.PostRequest{
			IdempotencyKey: "payout:" + id.String() + ":hold",
			Description:    "Payout " + id.String() + " to merchant " + m.ID.String(),
			Postings: []ledger.PostingInput{
				posting2(m.PayableAccountID, m.Currency, ledger.Debit, r.Amount),
				posting(sys.InTransit, ledger.Credit, r.Amount),
			},
		})
		if err != nil {
			return err
		}
		q := s.q.WithTx(tx)
		payout, err = q.InsertPayout(ctx, store.InsertPayoutParams{
			ID: id, MerchantID: m.ID, IdempotencyKey: r.IdempotencyKey, RequestHash: hash,
			Amount: r.Amount, Currency: m.Currency, HoldTxnID: txn.ID,
		})
		if err != nil {
			return err
		}
		return emit(ctx, q, m.ID, "payout", payout.ID, "payout.created", payoutFromRow(payout))
	})
	if errors.Is(err, pgx.ErrNoRows) { // a concurrent request with the same key won
		return s.replayPayout(ctx, r.MerchantID, r.IdempotencyKey, hash)
	}
	if err != nil {
		return Payout{}, mapError(err)
	}
	return payoutFromRow(payout), nil
}

func (s *Service) replayPayout(ctx context.Context, merchantID uuid.UUID, key, hash string) (Payout, error) {
	existing, err := s.q.GetPayoutByIdempotencyKey(ctx, store.GetPayoutByIdempotencyKeyParams{MerchantID: merchantID, IdempotencyKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return Payout{}, ErrNotFound
	}
	if err != nil {
		return Payout{}, err
	}
	if existing.RequestHash != hash {
		return Payout{}, fmt.Errorf("%w: %q", ErrIdempotencyKeyReused, key)
	}
	out := payoutFromRow(existing)
	out.Replayed = true
	return out, nil
}

func (s *Service) GetPayout(ctx context.Context, id uuid.UUID) (Payout, error) {
	p, err := s.q.GetPayout(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payout{}, fmt.Errorf("%w: payout %s", ErrNotFound, id)
	}
	if err != nil {
		return Payout{}, err
	}
	return payoutFromRow(p), nil
}

// CompletePayout records the bank's confirmation:
//
//	Dr payouts_in_transit  amount
//	  Cr psp_clearing        amount
func (s *Service) CompletePayout(ctx context.Context, id uuid.UUID) (Payout, error) {
	return s.finishPayout(ctx, id, PayoutPaid, "")
}

// FailPayout reverses the hold, returning the money to the merchant's
// available balance.
func (s *Service) FailPayout(ctx context.Context, id uuid.UUID, reason string) (Payout, error) {
	if reason == "" {
		reason = "bank_rejected"
	}
	return s.finishPayout(ctx, id, PayoutFailed, reason)
}

func (s *Service) finishPayout(ctx context.Context, id uuid.UUID, to, reason string) (Payout, error) {
	var payout store.Payout
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := q.GetPayoutForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: payout %s", ErrNotFound, id)
		}
		if err != nil {
			return err
		}
		if !canTransitionPayout(locked.Status, to) {
			return fmt.Errorf("%w: payout is already %s", ErrInvalidTransition, locked.Status)
		}

		var txn ledger.Transaction
		if to == PayoutPaid {
			sys, err := s.systemAccounts(ctx, locked.Currency)
			if err != nil {
				return err
			}
			txn, err = s.ledger.PostTx(ctx, tx, ledger.PostRequest{
				IdempotencyKey: "payout:" + id.String() + ":settle",
				Description:    "Payout " + id.String() + " confirmed by bank",
				Postings: []ledger.PostingInput{
					posting(sys.InTransit, ledger.Debit, locked.Amount),
					posting(sys.Clearing, ledger.Credit, locked.Amount),
				},
			})
			if err != nil {
				return err
			}
		} else {
			txn, err = s.ledger.ReverseTx(ctx, tx, ledger.ReverseRequest{
				TransactionID:  locked.HoldTxnID,
				IdempotencyKey: "payout:" + id.String() + ":reverse",
				Description:    "Payout " + id.String() + " failed: " + reason,
			})
			if err != nil {
				return err
			}
		}

		var failure *string
		if reason != "" {
			failure = &reason
		}
		payout, err = q.UpdatePayout(ctx, store.UpdatePayoutParams{
			ID: id, Status: to, FailureReason: failure, SettleTxnID: uuid.NullUUID{UUID: txn.ID, Valid: true},
		})
		if err != nil {
			return err
		}
		return emit(ctx, q, payout.MerchantID, "payout", payout.ID, "payout."+to, payoutFromRow(payout))
	})
	if err != nil {
		return Payout{}, mapError(err)
	}
	return payoutFromRow(payout), nil
}

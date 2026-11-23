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

const (
	RefundPending   = "pending"
	RefundSucceeded = "succeeded"
	RefundFailed    = "failed"
)

// Refund returns captured money to the customer. The platform returns its
// fee pro rata, so a full refund leaves both merchant and platform where
// they started:
//
//	Dr merchant payable  amount - fee share
//	Dr fee_revenue       fee share
//	  Cr psp_clearing      amount
//
// The ledger is posted when the refund is created, even if the provider
// settles it later: the money is committed to leave. If the provider later
// reports failure, the posting is reversed (see failRefund).
//
// Fails with ErrInsufficientFunds if the merchant has already been paid out.
func (s *Service) Refund(ctx context.Context, paymentID uuid.UUID, r RefundRequest) (Refund, error) {
	if r.IdempotencyKey == "" {
		return Refund{}, fmt.Errorf("%w: idempotency key is required", ErrInvalidRequest)
	}
	hash, err := requestHash(struct {
		PaymentID uuid.UUID
		RefundRequest
	}{paymentID, r})
	if err != nil {
		return Refund{}, err
	}

	var refund store.Refund
	var merchantID uuid.UUID
	replayed := false
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := s.lockPayment(ctx, q, paymentID)
		if err != nil {
			return err
		}
		merchantID = locked.MerchantID

		existing, err := q.GetRefundByIdempotencyKey(ctx, store.GetRefundByIdempotencyKeyParams{
			MerchantID: locked.MerchantID, IdempotencyKey: r.IdempotencyKey,
		})
		if err == nil {
			if existing.RequestHash != hash {
				return fmt.Errorf("%w: %q", ErrIdempotencyKeyReused, r.IdempotencyKey)
			}
			refund, replayed = existing, true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		status := Status(locked.Status)
		if status != StatusCaptured && status != StatusPartiallyRefunded {
			return fmt.Errorf("%w: cannot refund a %s payment", ErrInvalidTransition, locked.Status)
		}
		refundable := locked.AmountCaptured - locked.AmountRefunded
		amount := refundable
		if r.Amount != nil {
			amount = *r.Amount
		}
		if amount <= 0 || amount > refundable {
			return fmt.Errorf("%w: refund amount must be between 1 and %d", ErrInvalidRequest, refundable)
		}
		m, err := s.getMerchant(ctx, q, locked.MerchantID)
		if err != nil {
			return err
		}
		sys, err := s.systemAccounts(ctx, locked.Currency)
		if err != nil {
			return err
		}
		prov, err := s.providerFor(locked.Provider)
		if err != nil {
			return err
		}
		// Computed from cumulative totals so partial refunds can never return
		// more or less than the original fee in total.
		feeShare := proRata(locked.FeeAmount, locked.AmountRefunded+amount, locked.AmountCaptured) -
			proRata(locked.FeeAmount, locked.AmountRefunded, locked.AmountCaptured)

		refundID := uuid.New()
		txn, err := s.ledger.PostTx(ctx, tx, ledger.PostRequest{
			IdempotencyKey: "refund:" + refundID.String(),
			Description:    "Refund for payment " + paymentID.String(),
			Postings: nonZero(
				posting2(m.PayableAccountID, locked.Currency, ledger.Debit, amount-feeShare),
				posting(sys.Fees, ledger.Debit, feeShare),
				posting(sys.Clearing, ledger.Credit, amount),
			),
		})
		if err != nil {
			return err // insufficient funds is caught here, before the provider moves money
		}
		res, err := prov.Refund(ctx, deref(locked.ProviderPaymentID), refundID, amount)
		if err != nil {
			return providerError(err)
		}
		refundStatus := RefundSucceeded
		if res.Pending {
			refundStatus = RefundPending
		}
		refund, err = q.InsertRefund(ctx, store.InsertRefundParams{
			ID: refundID, PaymentID: paymentID, MerchantID: locked.MerchantID, IdempotencyKey: r.IdempotencyKey,
			RequestHash: hash, Amount: amount, Currency: locked.Currency, Reason: r.Reason,
			Status: refundStatus, LedgerTxnID: txn.ID, ProviderRefundID: &res.Reference,
		})
		if err != nil {
			return err // includes pgx.ErrNoRows: same key used concurrently on another payment
		}
		if err := emit(ctx, q, refund.MerchantID, "refund", refund.ID, "refund.created", refundFromRow(refund)); err != nil {
			return err
		}

		to := StatusPartiallyRefunded
		if locked.AmountRefunded+amount == locked.AmountCaptured {
			to = StatusRefunded
		}
		_, err = s.transition(ctx, q, locked, to,
			transitionOpts{amount: &amount, ledgerTxn: txn.ID, note: "refund " + refundID.String()},
			func(u *store.UpdatePaymentParams) { u.AmountRefunded = locked.AmountRefunded + amount })
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) && merchantID != uuid.Nil {
		return s.replayRefund(ctx, merchantID, r.IdempotencyKey, hash)
	}
	if err != nil {
		return Refund{}, mapError(err)
	}
	out := refundFromRow(refund)
	out.Replayed = replayed
	return out, nil
}

func (s *Service) replayRefund(ctx context.Context, merchantID uuid.UUID, key, hash string) (Refund, error) {
	existing, err := s.q.GetRefundByIdempotencyKey(ctx, store.GetRefundByIdempotencyKeyParams{MerchantID: merchantID, IdempotencyKey: key})
	if err != nil {
		return Refund{}, mapError(err)
	}
	if existing.RequestHash != hash {
		return Refund{}, fmt.Errorf("%w: %q", ErrIdempotencyKeyReused, key)
	}
	out := refundFromRow(existing)
	out.Replayed = true
	return out, nil
}

func (s *Service) ListRefunds(ctx context.Context, paymentID uuid.UUID) ([]Refund, error) {
	if _, err := s.GetPayment(ctx, paymentID); err != nil {
		return nil, err
	}
	rows, err := s.q.ListRefundsByPayment(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	out := make([]Refund, 0, len(rows))
	for _, r := range rows {
		out = append(out, refundFromRow(r))
	}
	return out, nil
}

// settleRefund marks a pending refund succeeded. Idempotent.
func (s *Service) settleRefund(ctx context.Context, providerRefundID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		refund, err := q.GetRefundByProviderRefForUpdate(ctx, &providerRefundID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // not ours
		}
		if err != nil || refund.Status != RefundPending {
			return err
		}
		updated, err := q.UpdateRefund(ctx, store.UpdateRefundParams{ID: refund.ID, Status: RefundSucceeded})
		if err != nil {
			return err
		}
		return emit(ctx, q, updated.MerchantID, "refund", updated.ID, "refund.succeeded", refundFromRow(updated))
	})
}

// failRefund handles a refund the provider could not complete: the ledger
// posting is reversed and the amount becomes refundable again. Idempotent.
func (s *Service) failRefund(ctx context.Context, providerRefundID, reason string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		refund, err := q.GetRefundByProviderRefForUpdate(ctx, &providerRefundID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil || refund.Status != RefundPending {
			return err
		}
		// Lock order: refund, then payment. Refund creation locks only the
		// payment, so the two paths cannot deadlock.
		p, err := s.lockPayment(ctx, q, refund.PaymentID)
		if err != nil {
			return err
		}
		reversal, err := s.ledger.ReverseTx(ctx, tx, ledger.ReverseRequest{
			TransactionID:  refund.LedgerTxnID,
			IdempotencyKey: "refund:" + refund.ID.String() + ":reverse",
			Description:    "Refund " + refund.ID.String() + " failed at provider",
		})
		if err != nil {
			return err
		}
		updated, err := q.UpdateRefund(ctx, store.UpdateRefundParams{
			ID: refund.ID, Status: RefundFailed, FailureReason: &reason,
			ReversalTxnID: uuid.NullUUID{UUID: reversal.ID, Valid: true},
		})
		if err != nil {
			return err
		}
		if err := emit(ctx, q, updated.MerchantID, "refund", updated.ID, "refund.failed", refundFromRow(updated)); err != nil {
			return err
		}
		remaining := p.AmountRefunded - refund.Amount
		to := StatusPartiallyRefunded
		if remaining == 0 {
			to = StatusCaptured
		}
		_, err = s.transition(ctx, q, p, to,
			transitionOpts{amount: &refund.Amount, ledgerTxn: reversal.ID, note: "refund " + refund.ID.String() + " failed: " + reason},
			func(u *store.UpdatePaymentParams) { u.AmountRefunded = remaining })
		return err
	})
}

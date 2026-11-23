package payments

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fincore/internal/ledger"
	"fincore/internal/provider"
	"fincore/internal/store"
)

// CreatePayment records the payment and starts it at the provider:
//
//   - synchronous providers (sandbox) authorize or decline immediately, and
//     automatic-capture payments are captured in the same call;
//   - asynchronous providers (Razorpay) return requires_action with a
//     next_action for the client; the result arrives via ConfirmPayment or a
//     webhook (ApplyProviderEvent).
//
// Retrying with the same idempotency key returns the same payment; if the
// first attempt never reached the provider, the retry resumes from there.
func (s *Service) CreatePayment(ctx context.Context, r CreatePaymentRequest) (Payment, error) {
	if r.CaptureMethod == "" {
		r.CaptureMethod = "automatic"
	}
	if r.Provider == "" {
		r.Provider = s.defaultProvider
	}
	if err := validateCreatePayment(r); err != nil {
		return Payment{}, err
	}
	if _, err := s.providerFor(r.Provider); err != nil {
		return Payment{}, err
	}
	m, err := s.getMerchant(ctx, s.q, r.MerchantID)
	if err != nil {
		return Payment{}, err
	}
	if r.Currency != m.Currency {
		return Payment{}, fmt.Errorf("%w: merchant settles in %s, payment is %s", ErrInvalidRequest, m.Currency, r.Currency)
	}
	hash, err := requestHash(r)
	if err != nil {
		return Payment{}, err
	}

	var p store.Payment
	replayed := false
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		p, err = q.InsertPayment(ctx, store.InsertPaymentParams{
			ID: uuid.New(), MerchantID: r.MerchantID, IdempotencyKey: r.IdempotencyKey, RequestHash: hash,
			Amount: r.Amount, Currency: r.Currency, CaptureMethod: r.CaptureMethod,
			PaymentMethod: r.PaymentMethod, Description: r.Description, Provider: r.Provider,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			p, err = q.GetPaymentByIdempotencyKey(ctx, store.GetPaymentByIdempotencyKeyParams{
				MerchantID: r.MerchantID, IdempotencyKey: r.IdempotencyKey,
			})
			if err != nil {
				return err
			}
			if p.RequestHash != hash {
				return fmt.Errorf("%w: %q", ErrIdempotencyKeyReused, r.IdempotencyKey)
			}
			replayed = true
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.InsertPaymentEvent(ctx, store.InsertPaymentEventParams{
			PaymentID: p.ID, ToStatus: string(StatusCreated), Amount: &p.Amount, Note: "payment created",
		}); err != nil {
			return err
		}
		return emit(ctx, q, p.MerchantID, "payment", p.ID, "payment.created", paymentFromRow(p))
	})
	if err != nil {
		return Payment{}, mapError(err)
	}

	if Status(p.Status) == StatusCreated && p.ProviderRef == nil {
		if p, err = s.start(ctx, p); err != nil {
			return Payment{}, err
		}
	}
	out, err := s.autoCapture(ctx, p)
	out.Replayed = replayed
	return out, err
}

// start opens the payment at the provider.
func (s *Service) start(ctx context.Context, p store.Payment) (store.Payment, error) {
	prov, err := s.providerFor(p.Provider)
	if err != nil {
		return store.Payment{}, err
	}
	res, err := prov.CreatePayment(ctx, provider.CreateRequest{
		PaymentID: p.ID, Amount: p.Amount, Currency: p.Currency, PaymentMethod: p.PaymentMethod, Description: p.Description,
	})
	switch {
	case errors.Is(err, provider.ErrUnknownPaymentMethod):
		res, err = provider.CreateResult{Outcome: provider.OutcomeDeclined, DeclineCode: "invalid_payment_method"}, nil
	case errors.Is(err, provider.ErrRejected):
		res, err = provider.CreateResult{Outcome: provider.OutcomeDeclined, DeclineCode: "provider_rejected"}, nil
	}
	if err != nil {
		// The payment stays "created" with no provider reference; a retry with
		// the same idempotency key resumes here.
		return store.Payment{}, providerError(err)
	}

	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := s.lockPayment(ctx, q, p.ID)
		if err != nil {
			return err
		}
		if Status(locked.Status) != StatusCreated || locked.ProviderRef != nil { // a concurrent retry got here first
			p = locked
			return nil
		}
		setRefs := func(u *store.UpdatePaymentParams) {
			if res.Reference != "" {
				u.ProviderRef = &res.Reference
			}
			if res.PaymentRef != "" {
				u.ProviderPaymentID = &res.PaymentRef
			}
		}
		switch res.Outcome {
		case provider.OutcomeAuthorized:
			p, err = s.transition(ctx, q, locked, StatusAuthorized, transitionOpts{note: "authorized by " + p.Provider}, setRefs)
		case provider.OutcomeDeclined:
			p, err = s.transition(ctx, q, locked, StatusFailed, transitionOpts{note: "declined: " + res.DeclineCode},
				func(u *store.UpdatePaymentParams) { setRefs(u); u.FailureReason = &res.DeclineCode })
		default:
			p, err = s.transition(ctx, q, locked, StatusCreated, transitionOpts{note: "awaiting customer at " + p.Provider},
				func(u *store.UpdatePaymentParams) { setRefs(u); u.NextAction = res.NextAction })
		}
		return err
	})
	if err != nil {
		return store.Payment{}, mapError(err)
	}
	return p, nil
}

// autoCapture captures an authorized automatic-capture payment.
func (s *Service) autoCapture(ctx context.Context, p store.Payment) (Payment, error) {
	if Status(p.Status) != StatusAuthorized || p.CaptureMethod != "automatic" {
		return paymentFromRow(p), nil
	}
	captured, err := s.Capture(ctx, p.ID, CaptureRequest{})
	if errors.Is(err, ErrInvalidTransition) { // lost a race with a concurrent retry or webhook
		return s.GetPayment(ctx, p.ID)
	}
	return captured, err
}

// ConfirmPayment completes an asynchronous payment using the signed result
// the provider's checkout returned to the browser.
func (s *Service) ConfirmPayment(ctx context.Context, id uuid.UUID, params map[string]string) (Payment, error) {
	p, err := s.q.GetPayment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, fmt.Errorf("%w: payment %s", ErrNotFound, id)
	}
	if err != nil {
		return Payment{}, err
	}
	prov, err := s.providerFor(p.Provider)
	if err != nil {
		return Payment{}, err
	}
	confirmer, ok := prov.(provider.Confirmer)
	if !ok {
		return Payment{}, fmt.Errorf("%w: %s payments do not need confirmation", ErrInvalidRequest, p.Provider)
	}
	if p.ProviderRef == nil {
		return Payment{}, fmt.Errorf("%w: payment has not been started at the provider", ErrInvalidTransition)
	}
	res, err := confirmer.Confirm(ctx, *p.ProviderRef, params)
	if err != nil {
		return Payment{}, providerError(err)
	}
	if res.Amount != p.Amount {
		return Payment{}, fmt.Errorf("%w: provider amount %d does not match payment amount %d", ErrInvalidRequest, res.Amount, p.Amount)
	}
	return s.applyAttempt(ctx, p.ID, res.Outcome, res.PaymentRef, res.DeclineCode, res.Captured)
}

// applyAttempt records the result of a customer's payment attempt, from
// either ConfirmPayment or a webhook. It is idempotent: once a payment has
// left "created", later reports of the same success are no-ops.
//
// A failed attempt does not fail the payment: Razorpay lets the customer
// retry the same order, so the payment stays "created" with the last
// failure recorded.
func (s *Service) applyAttempt(ctx context.Context, id uuid.UUID, outcome provider.Outcome, paymentRef, declineCode string, capturedAtProvider bool) (Payment, error) {
	var p store.Payment
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := s.lockPayment(ctx, q, id)
		if err != nil {
			return err
		}
		p = locked
		if Status(locked.Status) != StatusCreated {
			return nil
		}
		if outcome == provider.OutcomeAuthorized {
			p, err = s.transition(ctx, q, locked, StatusAuthorized, transitionOpts{note: "customer authorized " + paymentRef},
				func(u *store.UpdatePaymentParams) { u.ProviderPaymentID = &paymentRef; u.FailureReason = nil })
		} else {
			p, err = s.transition(ctx, q, locked, StatusCreated, transitionOpts{note: "attempt " + paymentRef + " failed: " + declineCode},
				func(u *store.UpdatePaymentParams) { u.FailureReason = &declineCode })
		}
		return err
	})
	if err != nil {
		return Payment{}, mapError(err)
	}
	// Money captured at the provider (auto-capture account setting, or a
	// dashboard capture) must be recorded even for manual-capture payments.
	if Status(p.Status) == StatusAuthorized && (p.CaptureMethod == "automatic" || capturedAtProvider) {
		captured, err := s.Capture(ctx, p.ID, CaptureRequest{})
		if errors.Is(err, ErrInvalidTransition) {
			return s.GetPayment(ctx, p.ID)
		}
		return captured, err
	}
	return paymentFromRow(p), nil
}

func (s *Service) GetPayment(ctx context.Context, id uuid.UUID) (Payment, error) {
	p, err := s.q.GetPayment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, fmt.Errorf("%w: payment %s", ErrNotFound, id)
	}
	if err != nil {
		return Payment{}, err
	}
	return paymentFromRow(p), nil
}

func (s *Service) ListPaymentEvents(ctx context.Context, id uuid.UUID) ([]PaymentEvent, error) {
	if _, err := s.GetPayment(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.q.ListPaymentEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	events := make([]PaymentEvent, 0, len(rows))
	for _, e := range rows {
		events = append(events, PaymentEvent{
			ID: e.ID, FromStatus: e.FromStatus, ToStatus: e.ToStatus, Amount: e.Amount,
			LedgerTxnID: nullUUID(e.LedgerTxnID), Note: e.Note, CreatedAt: e.CreatedAt,
		})
	}
	return events, nil
}

// Capture moves authorized funds into the ledger:
//
//	Dr psp_clearing       amount
//	  Cr merchant payable   amount - fee
//	  Cr fee_revenue        fee
func (s *Service) Capture(ctx context.Context, id uuid.UUID, r CaptureRequest) (Payment, error) {
	var p store.Payment
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := s.lockPayment(ctx, q, id)
		if err != nil {
			return err
		}
		if !CanTransition(Status(locked.Status), StatusCaptured) {
			return fmt.Errorf("%w: cannot capture a %s payment", ErrInvalidTransition, locked.Status)
		}
		amount := locked.Amount
		if r.Amount != nil {
			amount = *r.Amount
		}
		if amount <= 0 || amount > locked.Amount {
			return fmt.Errorf("%w: capture amount must be between 1 and %d", ErrInvalidRequest, locked.Amount)
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
		fee := Fee(amount, m.FeeBps)

		txn, err := s.ledger.PostTx(ctx, tx, ledger.PostRequest{
			IdempotencyKey: "payment:" + id.String() + ":capture",
			Description:    "Capture payment " + id.String(),
			Postings: nonZero(
				posting(sys.Clearing, ledger.Debit, amount),
				posting2(m.PayableAccountID, locked.Currency, ledger.Credit, amount-fee),
				posting(sys.Fees, ledger.Credit, fee),
			),
		})
		if err != nil {
			return err
		}
		// The row lock is held across the provider call so two captures of
		// the same payment cannot both reach the processor.
		if err := prov.Capture(ctx, deref(locked.ProviderPaymentID), amount, locked.Currency); err != nil {
			return providerError(err)
		}
		p, err = s.transition(ctx, q, locked, StatusCaptured,
			transitionOpts{amount: &amount, ledgerTxn: txn.ID, note: fmt.Sprintf("captured, fee %d", fee)},
			func(u *store.UpdatePaymentParams) {
				u.AmountCaptured = amount
				u.FeeAmount = fee
				u.CaptureTxnID = uuid.NullUUID{UUID: txn.ID, Valid: true}
			})
		return err
	})
	if err != nil {
		return Payment{}, mapError(err)
	}
	return paymentFromRow(p), nil
}

// Cancel voids an authorization that was never captured. No money moved, so
// nothing is posted to the ledger.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID) (Payment, error) {
	var p store.Payment
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := s.lockPayment(ctx, q, id)
		if err != nil {
			return err
		}
		if !CanTransition(Status(locked.Status), StatusCanceled) {
			return fmt.Errorf("%w: cannot cancel a %s payment", ErrInvalidTransition, locked.Status)
		}
		prov, err := s.providerFor(locked.Provider)
		if err != nil {
			return err
		}
		if err := prov.Void(ctx, deref(locked.ProviderPaymentID)); err != nil {
			return providerError(err)
		}
		p, err = s.transition(ctx, q, locked, StatusCanceled, transitionOpts{note: "authorization voided"}, nil)
		return err
	})
	if err != nil {
		return Payment{}, mapError(err)
	}
	return paymentFromRow(p), nil
}

func validateCreatePayment(r CreatePaymentRequest) error {
	switch {
	case r.IdempotencyKey == "":
		return fmt.Errorf("%w: idempotency key is required", ErrInvalidRequest)
	case r.MerchantID == uuid.Nil:
		return fmt.Errorf("%w: merchant_id is required", ErrInvalidRequest)
	case r.Amount <= 0 || r.Amount > MaxAmount:
		return fmt.Errorf("%w: amount must be between 1 and %d", ErrInvalidRequest, MaxAmount)
	case !ledger.ValidCurrency(r.Currency):
		return fmt.Errorf("%w: currency %q is invalid", ErrInvalidRequest, r.Currency)
	case r.CaptureMethod != "automatic" && r.CaptureMethod != "manual":
		return fmt.Errorf("%w: capture_method must be automatic or manual", ErrInvalidRequest)
	}
	return nil
}

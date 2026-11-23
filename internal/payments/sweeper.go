package payments

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"fincore/internal/metrics"
	"fincore/internal/provider"
	"fincore/internal/store"
)

type SweepResult struct {
	Recovered int
	Expired   int
}

// SweepStalePayments handles payments stuck in "created":
//
//   - after checkAfter, it asks providers that support it whether the
//     customer actually paid (the browser closed before /confirm and the
//     webhook was lost) and, if so, completes the payment;
//   - after expireAfter, unpaid payments fail with reason "expired".
//
// A payment the customer completes at the provider after expiry is caught by
// settlement reconciliation.
func (s *Service) SweepStalePayments(ctx context.Context, checkAfter, expireAfter time.Duration) (SweepResult, error) {
	var res SweepResult
	stale, err := s.q.ListStaleCreatedPayments(ctx, store.ListStaleCreatedPaymentsParams{
		OlderThanSeconds: checkAfter.Seconds(), Lim: 100,
	})
	if err != nil {
		return res, err
	}
	for _, p := range stale {
		recovered, err := s.recoverFromProvider(ctx, p)
		if err != nil {
			slog.WarnContext(ctx, "sweeper: provider check failed", "payment_id", p.ID, "err", err)
		}
		if recovered {
			metrics.SweeperPayments.WithLabelValues("recovered").Inc()
			res.Recovered++
			continue
		}
		if err == nil && time.Since(p.CreatedAt) >= expireAfter {
			expired, err := s.expire(ctx, p)
			if err != nil {
				return res, err
			}
			if expired {
				metrics.SweeperPayments.WithLabelValues("expired").Inc()
				res.Expired++
			}
		}
	}
	return res, nil
}

func (s *Service) recoverFromProvider(ctx context.Context, p store.Payment) (bool, error) {
	prov, err := s.providerFor(p.Provider)
	if err != nil {
		return false, err
	}
	checker, ok := prov.(provider.StatusChecker)
	if !ok || p.ProviderRef == nil {
		return false, nil
	}
	found, ok, err := checker.CheckPayment(ctx, *p.ProviderRef)
	if err != nil || !ok {
		return false, err
	}
	if found.Amount != p.Amount {
		return false, errors.New("provider amount does not match payment amount")
	}
	slog.InfoContext(ctx, "sweeper: recovered payment with lost callbacks", "payment_id", p.ID, "provider_payment_id", found.PaymentRef)
	_, err = s.applyAttempt(ctx, p.ID, provider.OutcomeAuthorized, found.PaymentRef, "", found.Captured)
	return err == nil, err
}

func (s *Service) expire(ctx context.Context, p store.Payment) (bool, error) {
	expired := false
	err := pgxBegin(ctx, s, func(q *store.Queries) error {
		locked, err := s.lockPayment(ctx, q, p.ID)
		if err != nil || Status(locked.Status) != StatusCreated {
			return err
		}
		reason := "expired"
		_, err = s.transition(ctx, q, locked, StatusFailed, transitionOpts{note: "expired awaiting customer"},
			func(u *store.UpdatePaymentParams) { u.FailureReason = &reason; u.NextAction = nil })
		expired = err == nil
		return err
	})
	return expired, mapError(err)
}

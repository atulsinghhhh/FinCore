package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"

	"fincore/internal/provider"
	"fincore/internal/store"
)

// ApplyProviderEvent applies a verified webhook. Providers deliver at least
// once and in any order, and the same fact may already have arrived via
// ConfirmPayment, so every branch is idempotent and order-tolerant.
// Events for payments or refunds we do not know are ignored.
func (s *Service) ApplyProviderEvent(ctx context.Context, providerName string, ev provider.Event) error {
	switch ev.Type {
	case provider.EventPaymentAuthorized, provider.EventPaymentCaptured, provider.EventPaymentFailed:
		p, err := s.q.GetPaymentByProviderRef(ctx, store.GetPaymentByProviderRefParams{Provider: providerName, ProviderRef: &ev.Reference})
		if errors.Is(err, pgx.ErrNoRows) {
			slog.InfoContext(ctx, "ignoring event for unknown payment", "provider", providerName, "event", ev.RawType, "ref", ev.Reference)
			return nil
		}
		if err != nil {
			return err
		}
		if ev.Type != provider.EventPaymentFailed && ev.Amount != p.Amount {
			return fmt.Errorf("%w: event amount %d does not match payment %s amount %d", ErrInvalidRequest, ev.Amount, p.ID, p.Amount)
		}
		outcome := provider.OutcomeAuthorized
		if ev.Type == provider.EventPaymentFailed {
			outcome = provider.OutcomeDeclined
		}
		_, err = s.applyAttempt(ctx, p.ID, outcome, ev.PaymentRef, ev.DeclineCode, ev.Type == provider.EventPaymentCaptured)
		return err

	case provider.EventRefundProcessed:
		return mapError(s.settleRefund(ctx, ev.RefundRef))

	case provider.EventRefundFailed:
		return mapError(s.failRefund(ctx, ev.RefundRef, "provider_refund_failed"))
	}
	return nil
}

// BeginWebhook records a delivery and reports whether it was already
// processed. Call FinishWebhook with the processing result.
func (s *Service) BeginWebhook(ctx context.Context, providerName string, ev provider.Event, payload []byte) (row store.WebhookEvent, alreadyProcessed bool, err error) {
	if !json.Valid(payload) {
		return row, false, fmt.Errorf("%w: webhook payload is not JSON", ErrInvalidRequest)
	}
	row, err = s.q.RecordWebhookEvent(ctx, store.RecordWebhookEventParams{
		Provider: providerName, EventID: ev.ID, EventType: ev.RawType, Payload: payload,
	})
	if err != nil {
		return row, false, err
	}
	return row, row.ProcessedAt != nil, nil
}

// RetryWebhook re-applies one due, previously failed inbound webhook.
// parse turns the stored payload back into an event for its provider.
// It reports whether there was anything to do.
func (s *Service) RetryWebhook(ctx context.Context, parse map[string]func(eventID string, body []byte) (provider.Event, error)) (bool, error) {
	found := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.ClaimRetryableWebhook(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		if err := q.BumpWebhookAttempts(ctx, row.ID); err != nil {
			return err
		}
		parser, ok := parse[row.Provider]
		if !ok {
			return fmt.Errorf("no parser for provider %q", row.Provider)
		}
		ev, err := parser(row.EventID, row.Payload)
		if err == nil {
			err = s.ApplyProviderEvent(ctx, row.Provider, ev)
		}
		// Record the result with the claim's connection so the row lock holds.
		if err == nil {
			return q.MarkWebhookProcessed(ctx, row.ID)
		}
		msg := err.Error()
		slog.WarnContext(ctx, "webhook retry failed", "event_id", row.EventID, "attempts", row.Attempts+1, "err", err)
		return q.MarkWebhookFailed(ctx, store.MarkWebhookFailedParams{ID: row.ID, LastError: &msg, RetryInSeconds: retryIn(err, row.Attempts+1)})
	})
	return found, err
}

// FinishWebhook records the processing result. Transient failures are
// scheduled for retry by the worker with exponential backoff; permanent ones
// (ErrInvalidRequest) are left for manual review.
func (s *Service) FinishWebhook(ctx context.Context, id int64, attempts int32, procErr error) error {
	if procErr == nil {
		return s.q.MarkWebhookProcessed(ctx, id)
	}
	msg := procErr.Error()
	return s.q.MarkWebhookFailed(ctx, store.MarkWebhookFailedParams{ID: id, LastError: &msg, RetryInSeconds: retryIn(procErr, attempts)})
}

// retryIn returns the backoff before the next attempt, or nil when the
// failure is permanent or attempts are exhausted.
func retryIn(err error, attempts int32) *float64 {
	if errors.Is(err, ErrInvalidRequest) || attempts >= MaxWebhookAttempts {
		return nil
	}
	secs := Backoff(int(attempts)).Seconds()
	return &secs
}

// MaxWebhookAttempts bounds retries of an inbound webhook.
const MaxWebhookAttempts = 10

// Backoff is exponential with full jitter: 5s, 10s, 20s ... capped at 1h.
func Backoff(attempt int) time.Duration {
	base := 5 * time.Second << min(attempt, 10)
	if base > time.Hour {
		base = time.Hour
	}
	return base/2 + time.Duration(mrand.Int64N(int64(base/2)))
}

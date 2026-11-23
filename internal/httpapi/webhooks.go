package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"fincore/internal/metrics"
	"fincore/internal/payments"
	"fincore/internal/provider/razorpay"
)

const webhookDedupeTTL = 7 * 24 * time.Hour

// razorpayWebhook receives Razorpay events. Razorpay retries any non-2xx
// response, so:
//   - bad signatures get 401 (never processed),
//   - duplicates and already-processed events get 200 without side effects,
//   - transient failures get 500 so Razorpay retries,
//   - events we can never apply (e.g. amount mismatch) get 200 and are kept
//     in webhook_events with last_error for manual review.
func (s *Server) razorpayWebhook(w http.ResponseWriter, r *http.Request) {
	if s.Razorpay == nil {
		writeError(w, http.StatusNotFound, "not_found", "razorpay is not configured")
		return
	}
	ctx := r.Context()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "could not read body")
		return
	}
	if !s.Razorpay.VerifyWebhook(body, r.Header.Get("X-Razorpay-Signature")) {
		writeError(w, http.StatusUnauthorized, "invalid_signature", "webhook signature verification failed")
		return
	}
	eventID := r.Header.Get("X-Razorpay-Event-Id")
	if eventID == "" {
		sum := sha256.Sum256(body)
		eventID = "body:" + hex.EncodeToString(sum[:])
	}
	ev, err := razorpay.ParseWebhook(eventID, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}

	cacheKey := "webhook:razorpay:" + eventID
	if s.Redis != nil {
		if n, err := s.Redis.Exists(ctx, cacheKey).Result(); err == nil && n == 1 {
			writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
			return
		}
	}

	row, processed, err := s.payments.BeginWebhook(ctx, "razorpay", ev, body)
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	if processed {
		s.markWebhookSeen(r, cacheKey)
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}

	procErr := s.payments.ApplyProviderEvent(ctx, "razorpay", ev)
	if err := s.payments.FinishWebhook(ctx, row.ID, row.Attempts, procErr); err != nil {
		slog.ErrorContext(ctx, "record webhook result", "err", err)
	}
	result := map[bool]string{true: "processed", false: "failed"}[procErr == nil]
	if procErr != nil && errors.Is(procErr, payments.ErrInvalidRequest) {
		result = "rejected"
	}
	metrics.ProviderWebhooks.WithLabelValues("razorpay", result).Inc()
	switch {
	case procErr == nil:
		s.markWebhookSeen(r, cacheKey)
		writeJSON(w, http.StatusOK, map[string]string{"status": "processed"})
	case errors.Is(procErr, payments.ErrInvalidRequest):
		slog.WarnContext(ctx, "webhook rejected, needs review", "event_id", eventID, "event", ev.RawType, "err", procErr)
		writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
	default:
		slog.ErrorContext(ctx, "webhook processing failed, provider will retry", "event_id", eventID, "err", procErr)
		writeError(w, http.StatusInternalServerError, "processing_failed", "will be retried")
	}
}

func (s *Server) markWebhookSeen(r *http.Request, key string) {
	if s.Redis == nil {
		return
	}
	if err := s.Redis.Set(r.Context(), key, 1, webhookDedupeTTL).Err(); err != nil {
		slog.WarnContext(r.Context(), "cache webhook dedupe key", "err", err)
	}
}

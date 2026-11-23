// Package outbox delivers outbox events to merchant webhook endpoints.
//
// Delivery is at-least-once: an event can be delivered again if the process
// dies after the merchant answered but before the result was recorded.
// Merchants deduplicate with the event ID (FinCore-Event-Id header).
//
// Order is best-effort: one dispatcher delivers a merchant's events in
// sequence, but retries and multiple dispatchers can reorder them. Each event
// carries the full object, so merchants should act on its state, not on the
// order of arrival.
package outbox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fincore/internal/metrics"
	"fincore/internal/payments"
	"fincore/internal/store"
)

const (
	SignatureHeader = "FinCore-Signature"
	EventIDHeader   = "FinCore-Event-Id"

	StatusPending   = "pending"
	StatusDelivered = "delivered"
	StatusSkipped   = "skipped"
	StatusDead      = "dead"
)

// Event is the JSON body merchants receive.
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

type Dispatcher struct {
	pool        *pgxpool.Pool
	q           *store.Queries
	HTTP        *http.Client
	BatchSize   int
	Concurrency int
	// MaxAttempts before an event is dead-lettered (about 1 day of retries).
	MaxAttempts int
	Backoff     func(attempt int) time.Duration
	// BlockedDelay is how long events held behind a failed one wait.
	BlockedDelay time.Duration
}

func NewDispatcher(pool *pgxpool.Pool) *Dispatcher {
	return &Dispatcher{
		pool:        pool,
		q:           store.New(pool),
		HTTP:        &http.Client{Timeout: 10 * time.Second},
		BatchSize:   50,
		Concurrency: 10,
		MaxAttempts: 12,
		Backoff:     payments.Backoff,

		BlockedDelay: time.Second,
	}
}

type result struct {
	status string
	err    error
}

// DispatchOnce claims a batch of due events, delivers them concurrently and
// records the outcomes. Claimed rows stay locked for the whole batch, so
// any number of dispatchers can run side by side.
func (d *Dispatcher) DispatchOnce(ctx context.Context) (int, error) {
	n := 0
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		q := d.q.WithTx(tx)
		rows, err := q.ClaimOutboxEvents(ctx, int32(d.BatchSize))
		if err != nil {
			return err
		}
		n = len(rows)

		// Merchants are delivered to concurrently; each merchant's events go
		// one at a time in order. After a failure, that merchant's remaining
		// events wait for the next attempt so they don't overtake it.
		results := make([]result, len(rows))
		byMerchant := map[uuid.UUID][]int{}
		for i, row := range rows {
			byMerchant[row.MerchantID] = append(byMerchant[row.MerchantID], i)
		}
		sem := make(chan struct{}, d.Concurrency)
		var wg sync.WaitGroup
		for _, idxs := range byMerchant {
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				var blocked error
				for _, i := range idxs {
					row := rows[i]
					switch {
					case row.WebhookUrl == nil || *row.WebhookUrl == "":
						results[i] = result{status: StatusSkipped}
					case blocked != nil:
						results[i] = result{err: fmt.Errorf("%w: %v", errBlocked, blocked)}
					default:
						results[i] = result{status: StatusDelivered, err: d.deliver(ctx, row)}
						blocked = results[i].err
					}
				}
			})
		}
		wg.Wait()

		for i, row := range rows {
			r := results[i]
			if r.err == nil {
				metrics.OutboxDeliveries.WithLabelValues(r.status).Inc()
				if err := q.MarkOutboxDelivered(ctx, store.MarkOutboxDeliveredParams{ID: row.ID, Status: r.status}); err != nil {
					return err
				}
				continue
			}
			if errors.Is(r.err, errBlocked) { // not attempted: retry soon without counting
				if err := q.DeferOutboxEvent(ctx, store.DeferOutboxEventParams{ID: row.ID, RetryInSeconds: d.BlockedDelay.Seconds()}); err != nil {
					return err
				}
				continue
			}
			attempts := int(row.Attempts) + 1
			status := StatusPending
			if attempts >= d.MaxAttempts {
				status = StatusDead
			}
			metrics.OutboxDeliveries.WithLabelValues(map[string]string{StatusPending: "failed", StatusDead: "dead"}[status]).Inc()
			msg := r.err.Error()
			if err := q.MarkOutboxFailed(ctx, store.MarkOutboxFailedParams{
				ID: row.ID, Status: status, LastError: &msg, RetryInSeconds: d.Backoff(attempts).Seconds(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

func (d *Dispatcher) deliver(ctx context.Context, row store.ClaimOutboxEventsRow) error {
	body, err := json.Marshal(Event{ID: EventID(row.ID), Type: row.EventType, CreatedAt: row.CreatedAt, Data: row.Payload})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *row.WebhookUrl, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "FinCore-Webhooks/1.0")
	req.Header.Set(EventIDHeader, EventID(row.ID))
	req.Header.Set(SignatureHeader, Sign(derefStr(row.WebhookSecret), time.Now(), body))

	resp, err := d.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("endpoint returned %d", resp.StatusCode)
	}
	return nil
}

func EventID(id int64) string { return "evt_" + strconv.FormatInt(id, 10) }

// Sign returns the FinCore-Signature header value: t=<unix>,v1=<hex hmac>,
// where the HMAC-SHA256 covers "<unix>.<body>". Binding the timestamp stops
// replays of old deliveries.
func Sign(secret string, at time.Time, body []byte) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

var (
	ErrInvalidSignature = errors.New("outbox: invalid signature")
	errBlocked          = errors.New("waiting for an earlier event to this merchant")
)

// Verify is what a merchant runs on receipt. tolerance bounds clock skew and
// replay age.
func Verify(secret, header string, body []byte, tolerance time.Duration, now time.Time) error {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" {
		return ErrInvalidSignature
	}
	if d := now.Sub(time.Unix(unix, 0)); d > tolerance || d < -tolerance {
		return fmt.Errorf("%w: timestamp outside tolerance", ErrInvalidSignature)
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), want) {
		return ErrInvalidSignature
	}
	return nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

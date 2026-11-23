package payments

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fincore/internal/store"
)

type Stats struct {
	Merchants           int64 `json:"merchants"`
	Payments            int64 `json:"payments"`
	CapturedVolume      int64 `json:"captured_volume"`
	RefundedVolume      int64 `json:"refunded_volume"`
	FeeVolume           int64 `json:"fee_volume"`
	FailedPayments      int64 `json:"failed_payments"`
	DeadEvents          int64 `json:"dead_outbox_events"`
	UnprocessedWebhooks int64 `json:"unprocessed_webhooks"`
	ReconExceptions     int64 `json:"reconciliation_exceptions"`
}

func (s *Service) Stats(ctx context.Context) (Stats, error) {
	r, err := s.q.DashboardStats(ctx)
	return Stats(r), err
}

type ListFilter struct {
	MerchantID *uuid.UUID
	Status     string
	// Cursor is the opaque next_cursor from a previous page; empty for the first.
	Cursor string
	Limit  int32
}

// Page is one page of a newest-first list. Pagination is keyset-based on
// (created_at, id), so rows are never skipped or repeated even when many
// share a timestamp or new rows arrive while paging.
type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

var maxUUID = uuid.UUID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	if c == "" {
		return time.Now().Add(24 * time.Hour), maxUUID, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err == nil {
		at, id, ok := strings.Cut(string(raw), "|")
		if ok {
			t, terr := time.Parse(time.RFC3339Nano, at)
			u, uerr := uuid.Parse(id)
			if terr == nil && uerr == nil {
				return t, u, nil
			}
		}
	}
	return time.Time{}, uuid.Nil, fmt.Errorf("%w: invalid cursor", ErrInvalidRequest)
}

func (f ListFilter) limit() int32 {
	if f.Limit <= 0 || f.Limit > 200 {
		return 50
	}
	return f.Limit
}

// page trims the extra row fetched to detect has_more and builds the cursor.
func page[T any](rows []T, limit int32, key func(T) (time.Time, uuid.UUID)) Page[T] {
	p := Page[T]{Data: rows}
	if int32(len(rows)) > limit {
		p.Data, p.HasMore = rows[:limit], true
		last := p.Data[len(p.Data)-1]
		c := encodeCursor(key(last))
		p.NextCursor = &c
	}
	if p.Data == nil {
		p.Data = []T{}
	}
	return p
}

func nullable(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func (s *Service) ListMerchants(ctx context.Context, limit int32) ([]Merchant, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.q.ListMerchants(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Merchant, 0, len(rows))
	for _, m := range rows {
		out = append(out, merchantFromRow(m))
	}
	return out, nil
}

func (s *Service) ListPayments(ctx context.Context, f ListFilter) (Page[Payment], error) {
	before, beforeID, err := decodeCursor(f.Cursor)
	if err != nil {
		return Page[Payment]{}, err
	}
	var status *string
	if f.Status != "" {
		status = &f.Status
	}
	rows, err := s.q.ListPayments(ctx, store.ListPaymentsParams{
		MerchantID: nullable(f.MerchantID), Status: status, Before: before, BeforeID: beforeID, Lim: f.limit() + 1,
	})
	if err != nil {
		return Page[Payment]{}, err
	}
	out := make([]Payment, 0, len(rows))
	for _, p := range rows {
		out = append(out, paymentFromRow(p))
	}
	return page(out, f.limit(), func(p Payment) (time.Time, uuid.UUID) { return p.CreatedAt, p.ID }), nil
}

func (s *Service) ListPayouts(ctx context.Context, f ListFilter) (Page[Payout], error) {
	before, beforeID, err := decodeCursor(f.Cursor)
	if err != nil {
		return Page[Payout]{}, err
	}
	rows, err := s.q.ListPayouts(ctx, store.ListPayoutsParams{MerchantID: nullable(f.MerchantID), Before: before, BeforeID: beforeID, Lim: f.limit() + 1})
	if err != nil {
		return Page[Payout]{}, err
	}
	out := make([]Payout, 0, len(rows))
	for _, p := range rows {
		out = append(out, payoutFromRow(p))
	}
	return page(out, f.limit(), func(p Payout) (time.Time, uuid.UUID) { return p.CreatedAt, p.ID }), nil
}

type OutboxEvent struct {
	ID            int64      `json:"id"`
	MerchantID    uuid.UUID  `json:"merchant_id"`
	AggregateType string     `json:"aggregate_type"`
	AggregateID   uuid.UUID  `json:"aggregate_id"`
	EventType     string     `json:"event_type"`
	Status        string     `json:"status"`
	Attempts      int32      `json:"attempts"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LastError     *string    `json:"last_error"`
	CreatedAt     time.Time  `json:"created_at"`
	DeliveredAt   *time.Time `json:"delivered_at"`
}

// ListOutbox pages newest-first by event id; next_cursor is the last id.
func (s *Service) ListOutbox(ctx context.Context, status string, merchantID *uuid.UUID, cursor string, limit int32) (Page[OutboxEvent], error) {
	beforeID := int64(1<<62 - 1)
	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || n <= 0 {
			return Page[OutboxEvent]{}, fmt.Errorf("%w: invalid cursor", ErrInvalidRequest)
		}
		beforeID = n
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var st *string
	if status != "" {
		st = &status
	}
	rows, err := s.q.ListOutboxEvents(ctx, store.ListOutboxEventsParams{Status: st, MerchantID: nullable(merchantID), BeforeID: beforeID, Lim: limit + 1})
	if err != nil {
		return Page[OutboxEvent]{}, err
	}
	out := make([]OutboxEvent, 0, len(rows))
	for _, e := range rows {
		out = append(out, OutboxEvent{
			ID: e.ID, MerchantID: e.MerchantID, AggregateType: e.AggregateType, AggregateID: e.AggregateID,
			EventType: e.EventType, Status: e.Status, Attempts: e.Attempts, NextAttemptAt: e.NextAttemptAt,
			LastError: e.LastError, CreatedAt: e.CreatedAt, DeliveredAt: e.DeliveredAt,
		})
	}
	p := Page[OutboxEvent]{Data: out}
	if int32(len(out)) > limit {
		p.Data, p.HasMore = out[:limit], true
		c := strconv.FormatInt(p.Data[len(p.Data)-1].ID, 10)
		p.NextCursor = &c
	}
	return p, nil
}

// RequeueOutbox sends a dead-lettered event back for delivery.
func (s *Service) RequeueOutbox(ctx context.Context, id int64) error {
	_, err := s.q.RequeueOutboxEvent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: dead outbox event %d", ErrNotFound, id)
	}
	return err
}

type OutboxStat struct {
	Status        string
	Count         int64
	OldestPending time.Time
}

func (s *Service) OutboxStats(ctx context.Context) ([]OutboxStat, error) {
	rows, err := s.q.OutboxStats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]OutboxStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, OutboxStat{Status: r.Status, Count: r.N, OldestPending: r.OldestPending})
	}
	return out, nil
}

// PaymentDetail is a payment with its audit trail and refunds.
type PaymentDetail struct {
	Payment
	Events  []PaymentEvent `json:"events"`
	Refunds []Refund       `json:"refunds"`
}

func (s *Service) GetPaymentDetail(ctx context.Context, id uuid.UUID) (PaymentDetail, error) {
	p, err := s.GetPayment(ctx, id)
	if err != nil {
		return PaymentDetail{}, err
	}
	events, err := s.ListPaymentEvents(ctx, id)
	if err != nil {
		return PaymentDetail{}, err
	}
	refunds, err := s.ListRefunds(ctx, id)
	if err != nil {
		return PaymentDetail{}, err
	}
	return PaymentDetail{Payment: p, Events: events, Refunds: refunds}, nil
}

// CreateMerchantWithKey creates a merchant and its first API key.
func (s *Service) CreateMerchantWithKey(ctx context.Context, r CreateMerchantRequest) (Merchant, APIKey, error) {
	m, err := s.CreateMerchant(ctx, r)
	if err != nil {
		return Merchant{}, APIKey{}, err
	}
	k, err := s.CreateAPIKey(ctx, m.ID)
	return m, k, err
}

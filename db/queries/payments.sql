-- name: CreateMerchant :one
INSERT INTO merchants (id, name, currency, fee_bps, payable_account_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetMerchant :one
SELECT * FROM merchants WHERE id = $1;

-- name: InsertPayment :one
INSERT INTO payments (
    id, merchant_id, idempotency_key, request_hash, amount, currency, status,
    capture_method, payment_method, description, provider
) VALUES ($1, $2, $3, $4, $5, $6, 'created', $7, $8, $9, $10)
ON CONFLICT (merchant_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetPayment :one
SELECT * FROM payments WHERE id = $1;

-- name: GetPaymentForUpdate :one
SELECT * FROM payments WHERE id = $1 FOR UPDATE;

-- name: GetPaymentByIdempotencyKey :one
SELECT * FROM payments WHERE merchant_id = $1 AND idempotency_key = $2;

-- name: UpdatePayment :one
UPDATE payments
SET status              = @status,
    provider_ref        = @provider_ref,
    provider_payment_id = @provider_payment_id,
    next_action         = @next_action,
    amount_captured = @amount_captured,
    amount_refunded = @amount_refunded,
    fee_amount      = @fee_amount,
    failure_reason  = @failure_reason,
    capture_txn_id  = @capture_txn_id,
    version         = version + 1,
    updated_at      = now()
WHERE id = @id
RETURNING *;

-- name: InsertPaymentEvent :exec
INSERT INTO payment_events (payment_id, from_status, to_status, amount, ledger_txn_id, note)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListPaymentEvents :many
SELECT * FROM payment_events WHERE payment_id = $1 ORDER BY id;

-- name: GetPaymentByProviderRef :one
SELECT * FROM payments WHERE provider = $1 AND provider_ref = $2;

-- name: InsertRefund :one
INSERT INTO refunds (id, payment_id, merchant_id, idempotency_key, request_hash, amount, currency, reason, status, ledger_txn_id, provider_refund_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (merchant_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetRefundByIdempotencyKey :one
SELECT * FROM refunds WHERE merchant_id = $1 AND idempotency_key = $2;

-- name: GetRefundByProviderRefForUpdate :one
SELECT * FROM refunds WHERE provider_refund_id = $1 FOR UPDATE;

-- name: UpdateRefund :one
UPDATE refunds
SET status          = @status,
    failure_reason  = @failure_reason,
    reversal_txn_id = @reversal_txn_id,
    updated_at      = now()
WHERE id = @id
RETURNING *;

-- name: ListRefundsByPayment :many
SELECT * FROM refunds WHERE payment_id = $1 ORDER BY created_at, id;

-- name: InsertPayout :one
INSERT INTO payouts (id, merchant_id, idempotency_key, request_hash, amount, currency, status, hold_txn_id)
VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)
ON CONFLICT (merchant_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetPayout :one
SELECT * FROM payouts WHERE id = $1;

-- name: GetPayoutForUpdate :one
SELECT * FROM payouts WHERE id = $1 FOR UPDATE;

-- name: GetPayoutByIdempotencyKey :one
SELECT * FROM payouts WHERE merchant_id = $1 AND idempotency_key = $2;

-- name: UpdatePayout :one
UPDATE payouts
SET status         = @status,
    failure_reason = @failure_reason,
    settle_txn_id  = @settle_txn_id,
    updated_at     = now()
WHERE id = @id
RETURNING *;

-- name: SumPendingPayouts :one
SELECT coalesce(sum(amount), 0)::bigint FROM payouts WHERE merchant_id = $1 AND status = 'pending';

-- name: ListStatusTransitions :many
SELECT * FROM status_transitions ORDER BY entity, from_status, to_status;

-- name: RecordWebhookEvent :one
INSERT INTO webhook_events (provider, event_id, event_type, payload)
VALUES ($1, $2, $3, $4)
ON CONFLICT (provider, event_id) DO UPDATE SET attempts = webhook_events.attempts + 1
RETURNING *;

-- name: MarkWebhookProcessed :exec
UPDATE webhook_events SET processed_at = now(), last_error = NULL WHERE id = $1;

-- Retry times use the database clock, the same one the claim queries
-- compare against, so app/DB clock skew cannot delay or hide a retry.
-- A NULL delay means "do not retry".
-- name: MarkWebhookFailed :exec
UPDATE webhook_events
SET last_error = @last_error,
    next_retry_at = now() + make_interval(secs => sqlc.narg(retry_in_seconds)::float8)
WHERE id = @id;

-- name: ClaimRetryableWebhook :one
SELECT * FROM webhook_events
WHERE processed_at IS NULL AND next_retry_at IS NOT NULL AND next_retry_at <= now()
ORDER BY next_retry_at
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: BumpWebhookAttempts :exec
UPDATE webhook_events SET attempts = attempts + 1 WHERE id = $1;

-- name: SetMerchantWebhook :one
UPDATE merchants SET webhook_url = $2, webhook_secret = $3 WHERE id = $1 RETURNING *;

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (merchant_id, aggregate_type, aggregate_id, event_type, payload)
VALUES ($1, $2, $3, $4, $5);

-- Claims due events; rows stay locked until the claiming transaction ends,
-- so concurrent workers never deliver the same event at the same time.
-- name: ClaimOutboxEvents :many
SELECT o.*, m.webhook_url, m.webhook_secret
FROM outbox_events o
JOIN merchants m ON m.id = o.merchant_id
WHERE o.status = 'pending' AND o.next_attempt_at <= now()
ORDER BY o.next_attempt_at, o.id
LIMIT $1
FOR UPDATE OF o SKIP LOCKED;

-- name: MarkOutboxDelivered :exec
UPDATE outbox_events SET status = @status, attempts = attempts + 1, delivered_at = now(), last_error = NULL
WHERE id = @id;

-- name: MarkOutboxFailed :exec
UPDATE outbox_events
SET status = @status, attempts = attempts + 1, last_error = @last_error,
    next_attempt_at = now() + make_interval(secs => @retry_in_seconds::float8)
WHERE id = @id;

-- name: DeferOutboxEvent :exec
UPDATE outbox_events SET next_attempt_at = now() + make_interval(secs => @retry_in_seconds::float8) WHERE id = @id;

-- name: ListOutboxEvents :many
SELECT * FROM outbox_events
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(merchant_id)::uuid IS NULL OR merchant_id = sqlc.narg(merchant_id))
  AND id < @before_id
ORDER BY id DESC
LIMIT @lim;

-- name: RequeueOutboxEvent :one
UPDATE outbox_events SET status = 'pending', next_attempt_at = now(), last_error = NULL
WHERE id = $1 AND status = 'dead'
RETURNING *;

-- name: ListStaleCreatedPayments :many
SELECT * FROM payments
WHERE status = 'created' AND created_at < now() - make_interval(secs => @older_than_seconds::float8)
ORDER BY created_at
LIMIT @lim;

-- name: OutboxStats :many
SELECT status, count(*)::bigint AS n, coalesce(min(created_at) FILTER (WHERE status = 'pending'), now())::timestamptz AS oldest_pending
FROM outbox_events GROUP BY status;

-- name: GetSettlementBatchByHash :one
SELECT * FROM settlement_batches WHERE provider = $1 AND file_sha256 = $2;

-- name: InsertSettlementBatch :one
INSERT INTO settlement_batches (provider, source, file_sha256, line_count)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: SetSettlementBatchSummary :one
UPDATE settlement_batches SET summary = $2 WHERE id = $1 RETURNING *;

-- name: GetSettlementBatch :one
SELECT * FROM settlement_batches WHERE id = $1;

-- name: ListSettlementBatches :many
SELECT * FROM settlement_batches ORDER BY created_at DESC LIMIT $1;

-- name: InsertSettlementLine :one
INSERT INTO settlement_lines (
    batch_id, provider, line_no, settlement_id, entity_type, entity_id, amount, fee, tax, currency,
    settled_at, result, detail, payment_id, refund_id, ledger_txn_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING *;

-- name: ListSettlementLines :many
SELECT * FROM settlement_lines WHERE batch_id = $1 ORDER BY line_no;

-- name: IsEntitySettled :one
SELECT EXISTS (
    SELECT 1 FROM settlement_lines
    WHERE provider = $1 AND entity_type = $2 AND entity_id = $3 AND result = 'matched'
);

-- name: GetPaymentByProviderPaymentID :one
SELECT * FROM payments WHERE provider = $1 AND provider_payment_id = $2;

-- name: GetRefundByProviderRef :one
SELECT * FROM refunds WHERE provider_refund_id = $1;

-- Captured payments with no matched settlement line, captured before a
-- cutoff (the processor should have settled them by now).
-- name: ListUnsettledCaptures :many
SELECT p.id, p.provider_payment_id, p.amount_captured, t.effective_at AS captured_at
FROM payments p
JOIN ledger_transactions t ON t.id = p.capture_txn_id
WHERE p.provider = $1
  AND t.effective_at < $2
  AND NOT EXISTS (
      SELECT 1 FROM settlement_lines l
      WHERE l.provider = p.provider AND l.entity_type = 'payment'
        AND l.entity_id = p.provider_payment_id AND l.result = 'matched'
  )
ORDER BY t.effective_at
LIMIT 500;

-- name: ListMerchants :many
SELECT * FROM merchants ORDER BY created_at DESC LIMIT $1;

-- name: ListPayments :many
SELECT * FROM payments
WHERE (sqlc.narg(merchant_id)::uuid IS NULL OR merchant_id = sqlc.narg(merchant_id))
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (created_at, id) < (@before::timestamptz, @before_id::uuid)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: ListPayouts :many
SELECT * FROM payouts
WHERE (sqlc.narg(merchant_id)::uuid IS NULL OR merchant_id = sqlc.narg(merchant_id))
  AND (created_at, id) < (@before::timestamptz, @before_id::uuid)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: DashboardStats :one
SELECT
    (SELECT count(*) FROM merchants)::bigint                                            AS merchants,
    (SELECT count(*) FROM payments)::bigint                                             AS payments,
    (SELECT coalesce(sum(amount_captured), 0) FROM payments)::bigint                    AS captured_volume,
    (SELECT coalesce(sum(amount_refunded), 0) FROM payments)::bigint                    AS refunded_volume,
    (SELECT coalesce(sum(fee_amount), 0) FROM payments)::bigint                         AS fee_volume,
    (SELECT count(*) FROM payments WHERE status = 'failed')::bigint                     AS failed_payments,
    (SELECT count(*) FROM outbox_events WHERE status = 'dead')::bigint                  AS dead_events,
    (SELECT count(*) FROM webhook_events WHERE processed_at IS NULL)::bigint            AS unprocessed_webhooks,
    (SELECT count(*) FROM settlement_lines WHERE result NOT IN ('matched'))::bigint     AS recon_exceptions;

-- name: InsertAPIKey :one
INSERT INTO api_keys (merchant_id, prefix, key_hash) VALUES ($1, $2, $3) RETURNING *;

-- name: GetActiveAPIKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = $1 AND revoked_at IS NULL;

-- Throttled: at most one write per key per minute.
-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: ListAPIKeys :many
SELECT * FROM api_keys WHERE merchant_id = $1 ORDER BY created_at DESC;

-- name: RevokeAPIKey :one
UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND merchant_id = $2 AND revoked_at IS NULL RETURNING *;

-- Calendar days in India Standard Time from @first_day to @last_day,
-- oldest first, including days with no payments.
-- name: DailyPaymentStats :many
WITH days AS (
    SELECT d::date AS day
    FROM generate_series(@first_day::date, @last_day::date, interval '1 day') AS d
)
SELECT
    days.day::date                                                           AS day,
    coalesce(sum(p.amount_captured), 0)::bigint                              AS captured_volume,
    count(p.id) FILTER (WHERE p.capture_txn_id IS NOT NULL)::bigint          AS succeeded,
    count(p.id) FILTER (WHERE p.status = 'failed')::bigint                   AS failed,
    count(p.id)::bigint                                                      AS total,
    coalesce(sum(p.amount_refunded), 0)::bigint                              AS refunded_volume,
    coalesce(sum(p.fee_amount), 0)::bigint                                   AS fees
FROM days
LEFT JOIN payments p ON (p.created_at AT TIME ZONE 'Asia/Kolkata')::date = days.day
GROUP BY days.day
ORDER BY days.day;

-- name: StatusBreakdown :many
SELECT status, count(*)::bigint AS n, coalesce(sum(amount), 0)::bigint AS amount
FROM payments
WHERE created_at >= @since::timestamptz
GROUP BY status
ORDER BY n DESC;

-- name: TopMerchants :many
SELECT m.id, m.name,
       coalesce(sum(p.amount_captured), 0)::bigint                     AS captured_volume,
       count(p.id) FILTER (WHERE p.capture_txn_id IS NOT NULL)::bigint AS succeeded
FROM payments p
JOIN merchants m ON m.id = p.merchant_id
WHERE p.created_at >= @since::timestamptz
GROUP BY m.id, m.name
ORDER BY captured_volume DESC
LIMIT @lim;

-- name: SearchPayments :many
SELECT * FROM payments
WHERE id::text LIKE @prefix || '%'
   OR provider_ref = @q
   OR provider_payment_id = @q
   OR description ILIKE '%' || @q || '%'
ORDER BY created_at DESC
LIMIT 8;

-- name: SearchMerchants :many
SELECT * FROM merchants
WHERE id::text LIKE @prefix || '%' OR lower(name) LIKE '%' || lower(@q) || '%'
ORDER BY name
LIMIT 8;

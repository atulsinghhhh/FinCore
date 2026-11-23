-- name: CreateAccount :one
INSERT INTO accounts (code, name, type, currency, allow_negative, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAccount :one
SELECT * FROM accounts WHERE id = $1;

-- name: GetAccountByCode :one
SELECT * FROM accounts WHERE code = $1;

-- name: GetAccountsByIDs :many
SELECT * FROM accounts WHERE id = ANY(@ids::uuid[]);

-- Locks balance rows in a fixed order (by account_id) so concurrent
-- transactions touching the same accounts cannot deadlock.
-- name: LockBalances :many
SELECT * FROM account_balances
WHERE account_id = ANY(@account_ids::uuid[])
ORDER BY account_id
FOR UPDATE;

-- name: GetBalance :one
SELECT * FROM account_balances WHERE account_id = $1;

-- name: GetBalanceAt :one
SELECT
    coalesce(sum(amount) FILTER (WHERE direction = 'debit'), 0)::bigint  AS debits_total,
    coalesce(sum(amount) FILTER (WHERE direction = 'credit'), 0)::bigint AS credits_total
FROM postings
WHERE account_id = @account_id AND effective_at <= @at;

-- name: InsertTransaction :one
INSERT INTO ledger_transactions (idempotency_key, request_hash, description, reverses_id, metadata, effective_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetTransaction :one
SELECT * FROM ledger_transactions WHERE id = $1;

-- name: GetTransactionByIdempotencyKey :one
SELECT * FROM ledger_transactions WHERE idempotency_key = $1;

-- name: InsertPostings :exec
INSERT INTO postings (transaction_id, account_id, currency, direction, amount, effective_at)
SELECT @transaction_id::uuid,
       unnest(@account_ids::uuid[]),
       unnest(@currencies::text[]),
       unnest(@directions::text[]),
       unnest(@amounts::bigint[]),
       now(); -- overwritten by trigger with the transaction's effective_at

-- name: ListPostingsByTransaction :many
SELECT * FROM postings WHERE transaction_id = $1 ORDER BY id;

-- name: ListBalanceMismatches :many
SELECT b.account_id,
       b.debits_total                     AS cached_debits,
       b.credits_total                    AS cached_credits,
       coalesce(p.debits, 0)::bigint      AS posted_debits,
       coalesce(p.credits, 0)::bigint     AS posted_credits
FROM account_balances b
LEFT JOIN (
    SELECT account_id,
           sum(amount) FILTER (WHERE direction = 'debit')  AS debits,
           sum(amount) FILTER (WHERE direction = 'credit') AS credits
    FROM postings
    GROUP BY account_id
) p ON p.account_id = b.account_id
WHERE b.debits_total  <> coalesce(p.debits, 0)
   OR b.credits_total <> coalesce(p.credits, 0);

-- name: TrialBalance :many
SELECT currency,
       coalesce(sum(amount) FILTER (WHERE direction = 'debit'), 0)::bigint  AS total_debits,
       coalesce(sum(amount) FILTER (WHERE direction = 'credit'), 0)::bigint AS total_credits
FROM postings
GROUP BY currency
ORDER BY currency;

-- name: ListAccountBalances :many
SELECT a.id, a.code, a.name, a.type, a.currency, b.debits_total, b.credits_total, b.balance
FROM accounts a
JOIN account_balances b ON b.account_id = a.id
WHERE (sqlc.narg(code_prefix)::text IS NULL OR a.code LIKE sqlc.narg(code_prefix) || '%')
ORDER BY a.code;

-- name: ListAccountPostings :many
SELECT p.id, p.transaction_id, p.direction, p.amount, p.currency, p.effective_at, t.description
FROM postings p
JOIN ledger_transactions t ON t.id = p.transaction_id
WHERE p.account_id = @account_id AND p.effective_at >= @from_at AND p.effective_at < @to_at
ORDER BY p.effective_at, p.id
LIMIT @lim;

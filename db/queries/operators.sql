-- name: CountOperators :one
SELECT count(*)::bigint FROM operators;

-- name: CreateOperator :one
INSERT INTO operators (email, name, role, password_hash) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetOperator :one
SELECT * FROM operators WHERE id = $1;

-- name: GetOperatorByEmail :one
SELECT * FROM operators WHERE email = $1;

-- name: ListOperators :many
SELECT * FROM operators ORDER BY created_at;

-- Locks every active admin row, so concurrent demotions cannot both pass
-- the "at least one admin remains" check.
-- name: LockActiveAdmins :many
SELECT id FROM operators WHERE role = 'admin' AND status = 'active' ORDER BY id FOR UPDATE;

-- name: GetOperatorForUpdate :one
SELECT * FROM operators WHERE id = $1 FOR UPDATE;

-- name: UpdateOperatorAccess :one
UPDATE operators SET role = $2, status = $3, session_version = session_version + 1 WHERE id = $1 RETURNING *;

-- name: TouchOperatorLogin :exec
UPDATE operators SET last_login_at = now() WHERE id = $1;

-- name: RevokeOpenInvitesForEmail :exec
UPDATE operator_invites SET revoked_at = now() WHERE email = $1 AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: CreateInvite :one
INSERT INTO operator_invites (email, role, token_hash, invited_by, expires_at)
VALUES ($1, $2, $3, $4, now() + make_interval(hours => @valid_hours::int))
RETURNING *;

-- name: GetInviteByTokenHash :one
SELECT i.*, o.name AS invited_by_name
FROM operator_invites i LEFT JOIN operators o ON o.id = i.invited_by
WHERE i.token_hash = $1;

-- name: LockInviteByTokenHash :one
SELECT * FROM operator_invites WHERE token_hash = $1 FOR UPDATE;

-- name: MarkInviteAccepted :exec
UPDATE operator_invites SET accepted_at = now() WHERE id = $1;

-- name: RevokeInvite :one
UPDATE operator_invites SET revoked_at = now() WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL RETURNING *;

-- name: ListOpenInvites :many
SELECT i.*, o.name AS invited_by_name
FROM operator_invites i LEFT JOIN operators o ON o.id = i.invited_by
WHERE i.accepted_at IS NULL AND i.revoked_at IS NULL
ORDER BY i.created_at DESC;

-- name: InsertOperatorEvent :exec
INSERT INTO operator_events (actor_id, action, target, detail) VALUES ($1, $2, $3, $4);

-- name: ListOperatorEvents :many
SELECT e.*, o.name AS actor_name
FROM operator_events e LEFT JOIN operators o ON o.id = e.actor_id
ORDER BY e.id DESC
LIMIT $1;

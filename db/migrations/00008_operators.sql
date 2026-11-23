-- +goose Up

-- Dashboard operators: invite-only accounts with two roles.
--   admin  - full access, including inviting and managing operators
--   viewer - read-only
CREATE TABLE operators (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email           text NOT NULL UNIQUE CHECK (email = lower(email) AND email LIKE '%_@_%'),
    name            text NOT NULL CHECK (name <> ''),
    role            text NOT NULL CHECK (role IN ('admin', 'viewer')),
    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    password_hash   text NOT NULL,
    -- Bumped on role change or disable; sessions carrying an older version
    -- are rejected, so changes take effect immediately.
    session_version integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_login_at   timestamptz
);

-- Invitations. Only a SHA-256 of the token is stored; the link is shown once.
CREATE TABLE operator_invites (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email       text NOT NULL CHECK (email = lower(email)),
    role        text NOT NULL CHECK (role IN ('admin', 'viewer')),
    token_hash  text NOT NULL UNIQUE,
    invited_by  uuid REFERENCES operators (id),
    expires_at  timestamptz NOT NULL,
    accepted_at timestamptz,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- At most one open invite per email; re-inviting revokes the old one.
CREATE UNIQUE INDEX operator_invites_open_idx ON operator_invites (email) WHERE accepted_at IS NULL AND revoked_at IS NULL;

-- Append-only audit trail of who did what to whom.
CREATE TABLE operator_events (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id   uuid REFERENCES operators (id),
    action     text NOT NULL,
    target     text NOT NULL,
    detail     text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER operator_events_append_only
    BEFORE UPDATE OR DELETE ON operator_events
    FOR EACH ROW EXECUTE FUNCTION ledger_reject_mutation();

-- +goose Down
DROP TABLE operator_events;
DROP TABLE operator_invites;
DROP TABLE operators;

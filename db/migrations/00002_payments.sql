-- +goose Up

-- Payments, refunds and payouts. Money movement for all of these goes through
-- the ledger; these tables hold lifecycle state and link to ledger transactions.

-- The allowed state transitions, as data. The Go state machine must match
-- this table exactly (a test asserts it), and a trigger enforces it on every
-- UPDATE, so no code path can skip a state.
CREATE TABLE status_transitions (
    entity      text NOT NULL,
    from_status text NOT NULL,
    to_status   text NOT NULL,
    PRIMARY KEY (entity, from_status, to_status)
);

INSERT INTO status_transitions (entity, from_status, to_status) VALUES
    ('payment', 'created',            'authorized'),
    ('payment', 'created',            'failed'),
    ('payment', 'authorized',         'captured'),
    ('payment', 'authorized',         'canceled'),
    ('payment', 'captured',           'partially_refunded'),
    ('payment', 'captured',           'refunded'),
    ('payment', 'partially_refunded', 'partially_refunded'),
    ('payment', 'partially_refunded', 'refunded'),
    ('payout',  'pending',            'paid'),
    ('payout',  'pending',            'failed');

-- +goose StatementBegin
CREATE FUNCTION guard_status_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status AND NOT EXISTS (
        SELECT 1 FROM status_transitions
        WHERE entity = TG_ARGV[0] AND from_status = OLD.status AND to_status = NEW.status
    ) THEN
        RAISE EXCEPTION 'invalid % transition: % -> %', TG_ARGV[0], OLD.status, NEW.status
            USING ERRCODE = 'FC003';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TABLE merchants (
    id                 uuid PRIMARY KEY,
    name               text NOT NULL CHECK (name <> ''),
    currency           text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    -- Platform fee in basis points (200 = 2%).
    fee_bps            integer NOT NULL CHECK (fee_bps BETWEEN 0 AND 10000),
    payable_account_id uuid NOT NULL UNIQUE REFERENCES accounts (id),
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE payments (
    id              uuid PRIMARY KEY,
    merchant_id     uuid NOT NULL REFERENCES merchants (id),
    idempotency_key text NOT NULL,
    request_hash    text NOT NULL,
    amount          bigint NOT NULL CHECK (amount > 0),
    currency        text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    status          text NOT NULL CHECK (status IN
                        ('created', 'authorized', 'captured', 'partially_refunded', 'refunded', 'canceled', 'failed')),
    capture_method  text NOT NULL CHECK (capture_method IN ('automatic', 'manual')),
    payment_method  text NOT NULL,
    description     text NOT NULL DEFAULT '',
    provider        text NOT NULL,
    provider_ref    text,
    amount_captured bigint NOT NULL DEFAULT 0,
    amount_refunded bigint NOT NULL DEFAULT 0,
    fee_amount      bigint NOT NULL DEFAULT 0,
    failure_reason  text,
    capture_txn_id  uuid REFERENCES ledger_transactions (id),
    version         bigint NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, idempotency_key),
    CHECK (amount_captured BETWEEN 0 AND amount),
    CHECK (amount_refunded BETWEEN 0 AND amount_captured),
    CHECK (fee_amount BETWEEN 0 AND amount_captured)
);

CREATE INDEX payments_merchant_created_idx ON payments (merchant_id, created_at DESC);

CREATE TRIGGER payments_guard_status_transition
    BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION guard_status_transition('payment');

-- Append-only audit trail of every payment state change.
CREATE TABLE payment_events (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    payment_id    uuid NOT NULL REFERENCES payments (id),
    from_status   text,
    to_status     text NOT NULL,
    amount        bigint,
    ledger_txn_id uuid REFERENCES ledger_transactions (id),
    note          text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX payment_events_payment_idx ON payment_events (payment_id, id);

CREATE TRIGGER payment_events_append_only
    BEFORE UPDATE OR DELETE ON payment_events
    FOR EACH ROW EXECUTE FUNCTION ledger_reject_mutation();

CREATE TABLE refunds (
    id              uuid PRIMARY KEY,
    payment_id      uuid NOT NULL REFERENCES payments (id),
    merchant_id     uuid NOT NULL REFERENCES merchants (id),
    idempotency_key text NOT NULL,
    request_hash    text NOT NULL,
    amount          bigint NOT NULL CHECK (amount > 0),
    currency        text NOT NULL,
    reason          text NOT NULL DEFAULT '',
    status          text NOT NULL CHECK (status IN ('succeeded')),
    ledger_txn_id   uuid NOT NULL REFERENCES ledger_transactions (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, idempotency_key)
);

CREATE INDEX refunds_payment_idx ON refunds (payment_id, created_at);

CREATE TABLE payouts (
    id              uuid PRIMARY KEY,
    merchant_id     uuid NOT NULL REFERENCES merchants (id),
    idempotency_key text NOT NULL,
    request_hash    text NOT NULL,
    amount          bigint NOT NULL CHECK (amount > 0),
    currency        text NOT NULL,
    status          text NOT NULL CHECK (status IN ('pending', 'paid', 'failed')),
    failure_reason  text,
    hold_txn_id     uuid NOT NULL REFERENCES ledger_transactions (id),
    settle_txn_id   uuid REFERENCES ledger_transactions (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, idempotency_key)
);

CREATE INDEX payouts_merchant_created_idx ON payouts (merchant_id, created_at DESC);

CREATE TRIGGER payouts_guard_status_transition
    BEFORE UPDATE ON payouts
    FOR EACH ROW EXECUTE FUNCTION guard_status_transition('payout');

-- +goose Down
DROP TABLE payouts;
DROP TABLE refunds;
DROP TABLE payment_events;
DROP TABLE payments;
DROP TABLE merchants;
DROP FUNCTION guard_status_transition();
DROP TABLE status_transitions;

-- +goose Up

-- Asynchronous providers (Razorpay): the customer authorizes in the
-- provider's checkout, and we learn the outcome from a signed callback or a
-- webhook. Refunds can also settle (or fail) after we create them.

-- provider_ref is the provider's order ID; provider_payment_id is the ID of
-- the customer's payment attempt, known once they have paid.
ALTER TABLE payments
    ADD COLUMN provider_payment_id text,
    ADD COLUMN next_action jsonb;

CREATE UNIQUE INDEX payments_provider_ref_idx ON payments (provider, provider_ref) WHERE provider_ref IS NOT NULL;

ALTER TABLE refunds
    DROP CONSTRAINT refunds_status_check,
    ADD CONSTRAINT refunds_status_check CHECK (status IN ('pending', 'succeeded', 'failed')),
    ADD COLUMN provider_refund_id text,
    ADD COLUMN failure_reason text,
    ADD COLUMN reversal_txn_id uuid REFERENCES ledger_transactions (id),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

CREATE UNIQUE INDEX refunds_provider_refund_idx ON refunds (provider_refund_id) WHERE provider_refund_id IS NOT NULL;

INSERT INTO status_transitions (entity, from_status, to_status) VALUES
    ('refund',  'pending',            'succeeded'),
    ('refund',  'pending',            'failed'),
    -- A refund the provider later rejects hands the money back to the payment.
    ('payment', 'refunded',           'partially_refunded'),
    ('payment', 'refunded',           'captured'),
    ('payment', 'partially_refunded', 'captured');

CREATE TRIGGER refunds_guard_status_transition
    BEFORE UPDATE ON refunds
    FOR EACH ROW EXECUTE FUNCTION guard_status_transition('refund');

-- Every webhook delivery we accept, for deduplication and audit. Providers
-- deliver at least once; (provider, event_id) makes processing exactly-once.
CREATE TABLE webhook_events (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider     text NOT NULL,
    event_id     text NOT NULL,
    event_type   text NOT NULL,
    payload      jsonb NOT NULL,
    attempts     integer NOT NULL DEFAULT 1,
    last_error   text,
    received_at  timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    UNIQUE (provider, event_id)
);

-- +goose Down
DROP TABLE webhook_events;
DROP TRIGGER refunds_guard_status_transition ON refunds;
DELETE FROM status_transitions WHERE entity = 'refund'
    OR (entity = 'payment' AND to_status IN ('partially_refunded', 'captured') AND from_status IN ('refunded', 'partially_refunded') AND NOT (from_status = 'partially_refunded' AND to_status = 'partially_refunded'));
DROP INDEX refunds_provider_refund_idx;
ALTER TABLE refunds
    DROP COLUMN updated_at,
    DROP COLUMN reversal_txn_id,
    DROP COLUMN failure_reason,
    DROP COLUMN provider_refund_id,
    DROP CONSTRAINT refunds_status_check,
    ADD CONSTRAINT refunds_status_check CHECK (status IN ('succeeded'));
DROP INDEX payments_provider_ref_idx;
ALTER TABLE payments DROP COLUMN next_action, DROP COLUMN provider_payment_id;

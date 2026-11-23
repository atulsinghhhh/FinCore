-- +goose Up

-- Transactional outbox: every business event is written in the same DB
-- transaction as the state change that caused it, then delivered to the
-- merchant's webhook endpoint by the worker. No event is lost if the process
-- crashes after commit, and no event is sent for a change that rolled back.
CREATE TABLE outbox_events (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    merchant_id     uuid NOT NULL REFERENCES merchants (id),
    aggregate_type  text NOT NULL,
    aggregate_id    uuid NOT NULL,
    event_type      text NOT NULL,
    payload         jsonb NOT NULL,
    status          text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'delivered', 'skipped', 'dead')),
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz
);

CREATE INDEX outbox_due_idx ON outbox_events (next_attempt_at, id) WHERE status = 'pending';
CREATE INDEX outbox_merchant_idx ON outbox_events (merchant_id, id);
CREATE INDEX outbox_dead_idx ON outbox_events (id) WHERE status = 'dead';

ALTER TABLE merchants
    ADD COLUMN webhook_url    text,
    ADD COLUMN webhook_secret text;

-- Inbound webhooks that failed transiently are retried by the worker.
ALTER TABLE webhook_events ADD COLUMN next_retry_at timestamptz;
CREATE INDEX webhook_events_retry_idx ON webhook_events (next_retry_at) WHERE processed_at IS NULL;

-- The sweeper looks for payments still waiting on the customer.
CREATE INDEX payments_created_idx ON payments (created_at) WHERE status = 'created';

-- +goose Down
DROP INDEX payments_created_idx;
DROP INDEX webhook_events_retry_idx;
ALTER TABLE webhook_events DROP COLUMN next_retry_at;
ALTER TABLE merchants DROP COLUMN webhook_secret, DROP COLUMN webhook_url;
DROP TABLE outbox_events;

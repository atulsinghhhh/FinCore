-- +goose Up

-- Settlement reconciliation: the processor's settlement report is imported
-- and every line is matched against our records. Matched lines move money
-- from psp_clearing to the bank in the ledger; everything else is an
-- exception for review.
CREATE TABLE settlement_batches (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider    text NOT NULL,
    source      text NOT NULL,
    file_sha256 text NOT NULL,
    line_count  integer NOT NULL,
    summary     jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, file_sha256)
);

CREATE TABLE settlement_lines (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id      uuid NOT NULL REFERENCES settlement_batches (id),
    provider      text NOT NULL,
    line_no       integer NOT NULL,
    settlement_id text NOT NULL,
    entity_type   text NOT NULL,
    entity_id     text NOT NULL,
    amount        bigint NOT NULL,
    fee           bigint NOT NULL DEFAULT 0,
    tax           bigint NOT NULL DEFAULT 0,
    currency      text NOT NULL,
    settled_at    timestamptz NOT NULL,
    result        text NOT NULL CHECK (result IN
                      ('matched', 'missing_in_ledger', 'amount_mismatch', 'status_mismatch', 'duplicate', 'unsupported')),
    detail        text NOT NULL DEFAULT '',
    payment_id    uuid REFERENCES payments (id),
    refund_id     uuid REFERENCES refunds (id),
    ledger_txn_id uuid REFERENCES ledger_transactions (id)
);

CREATE INDEX settlement_lines_batch_idx ON settlement_lines (batch_id, line_no);
-- A provider entity can be settled (matched) only once, across all files.
CREATE UNIQUE INDEX settlement_lines_matched_idx ON settlement_lines (provider, entity_type, entity_id) WHERE result = 'matched';

CREATE TRIGGER settlement_lines_append_only
    BEFORE UPDATE OR DELETE ON settlement_lines
    FOR EACH ROW EXECUTE FUNCTION ledger_reject_mutation();

CREATE INDEX payments_provider_payment_idx ON payments (provider, provider_payment_id) WHERE provider_payment_id IS NOT NULL;

-- +goose Down
DROP INDEX payments_provider_payment_idx;
DROP TABLE settlement_lines;
DROP TABLE settlement_batches;

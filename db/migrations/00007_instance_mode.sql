-- +goose Up

-- Which mode this database belongs to. Written once, on the first start.
-- The API refuses to start if its configured mode differs, so a live server
-- can never be pointed at test data (or the reverse).
CREATE TABLE instance_settings (
    singleton  boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    mode       text NOT NULL CHECK (mode IN ('test', 'live')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER instance_settings_immutable
    BEFORE UPDATE OR DELETE ON instance_settings
    FOR EACH ROW EXECUTE FUNCTION ledger_reject_mutation();

-- +goose Down
DROP TABLE instance_settings;

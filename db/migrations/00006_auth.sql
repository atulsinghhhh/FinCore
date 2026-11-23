-- +goose Up

-- Merchant API keys. Only a SHA-256 hash is stored: keys are 256-bit random
-- values, so a fast hash is safe (no dictionary to attack) and lets every
-- request authenticate with one indexed lookup. The prefix is kept for
-- display ("fc_test_ab12…").
CREATE TABLE api_keys (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  uuid NOT NULL REFERENCES merchants (id),
    prefix       text NOT NULL,
    key_hash     text NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);

CREATE INDEX api_keys_merchant_idx ON api_keys (merchant_id);

-- +goose Down
DROP TABLE api_keys;

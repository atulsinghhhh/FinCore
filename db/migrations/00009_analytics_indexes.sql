-- +goose Up
-- Supports the dashboard's time-series analytics and global search.
CREATE INDEX payments_created_at_idx ON payments (created_at);
CREATE INDEX payments_provider_ref_lookup_idx ON payments (provider_ref) WHERE provider_ref IS NOT NULL;
CREATE INDEX merchants_name_lower_idx ON merchants (lower(name));

-- +goose Down
DROP INDEX merchants_name_lower_idx;
DROP INDEX payments_provider_ref_lookup_idx;
DROP INDEX payments_created_at_idx;

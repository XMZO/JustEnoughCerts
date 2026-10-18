-- +goose Up
-- The last test of an instance (POST /api/v1/providers/{id}/test): when,
-- and what it said, as JSON {"ok", "key", "params"}; NULL until it is
-- tested, and again once its settings change.
ALTER TABLE kernel_providers ADD COLUMN tested_at INTEGER;
ALTER TABLE kernel_providers ADD COLUMN test_result TEXT;

-- +goose Down
ALTER TABLE kernel_providers DROP COLUMN test_result;
ALTER TABLE kernel_providers DROP COLUMN tested_at;

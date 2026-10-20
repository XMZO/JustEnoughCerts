-- +goose Up
-- incomplete is 1 when a domain served the version in use without the
-- intermediates clients need; chain_alerted is the fingerprint of the
-- version that was told of, so that it is told once.
ALTER TABLE monitor_results ADD COLUMN incomplete INTEGER NOT NULL DEFAULT 0;
ALTER TABLE monitor_results ADD COLUMN chain_alerted TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE monitor_results DROP COLUMN chain_alerted;
ALTER TABLE monitor_results DROP COLUMN incomplete;

-- +goose Up
-- tls_version is the TLS version a domain spoke, as "1.3"; legacy_tls is 1
-- when it still takes TLS 1.0 or 1.1, which browsers dropped. Both are
-- told for the version in use only.
ALTER TABLE monitor_results ADD COLUMN tls_version TEXT NOT NULL DEFAULT '';
ALTER TABLE monitor_results ADD COLUMN legacy_tls INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE monitor_results DROP COLUMN legacy_tls;
ALTER TABLE monitor_results DROP COLUMN tls_version;

-- +goose Up
-- revoked_at is when JEC had the version revoked at its CA, NULL while it
-- has not; revoke_reason is the reason it gave, as the API names it.
ALTER TABLE certs_versions ADD COLUMN revoked_at INTEGER;
ALTER TABLE certs_versions ADD COLUMN revoke_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE certs_versions DROP COLUMN revoke_reason;
ALTER TABLE certs_versions DROP COLUMN revoked_at;

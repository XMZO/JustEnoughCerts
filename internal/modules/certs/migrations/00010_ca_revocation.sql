-- +goose Up
-- What the CA tells of a version through OCSP or its CRL (revocation.go):
-- ca_revoked_at is when the CA revoked it, NULL while it has not, and
-- ca_revoke_reason the reason code of RFC 5280; status_check is when to
-- ask again. Times are Unix milliseconds.
ALTER TABLE certs_versions ADD COLUMN ca_revoked_at INTEGER;
ALTER TABLE certs_versions ADD COLUMN ca_revoke_reason INTEGER NOT NULL DEFAULT 0;
ALTER TABLE certs_versions ADD COLUMN status_check INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE certs_versions DROP COLUMN status_check;
ALTER TABLE certs_versions DROP COLUMN ca_revoke_reason;
ALTER TABLE certs_versions DROP COLUMN ca_revoked_at;

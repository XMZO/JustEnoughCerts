-- +goose Up
-- reuse_key keeps the private key of the version in use for its renewals,
-- as a key pinned by an app or published in a TLSA record needs.
ALTER TABLE certs_certificates ADD COLUMN reuse_key INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE certs_certificates DROP COLUMN reuse_key;

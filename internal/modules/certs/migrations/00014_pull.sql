-- +goose Up
-- The pull address of a certificate: servers JEC cannot reach fetch its
-- version in use with the key whose SHA-256 is key_hash, in the
-- Authorization header. used_at is the last fetch. Times are Unix
-- milliseconds.
CREATE TABLE certs_pull (
    certificate_id INTEGER PRIMARY KEY REFERENCES certs_certificates (id) ON DELETE CASCADE,
    key_hash       BLOB NOT NULL UNIQUE,
    created_at     INTEGER NOT NULL,
    used_at        INTEGER
) STRICT;

-- +goose Down
DROP TABLE certs_pull;

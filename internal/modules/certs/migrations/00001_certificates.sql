-- +goose Up
-- A certificate is what JEC keeps valid: a set of domains with a source and
-- a renewal policy. Each certificate issued or uploaded for it is a
-- version. Times are Unix milliseconds; lists are JSON arrays.
CREATE TABLE certs_certificates (
    id                 INTEGER PRIMARY KEY,
    domains            TEXT NOT NULL,
    source             TEXT NOT NULL,
    key_type           TEXT NOT NULL,
    renew_before_days  INTEGER NOT NULL,
    current_version_id INTEGER REFERENCES certs_versions (id) ON DELETE SET NULL,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
) STRICT;

-- sealed_key is the PKCS #8 PEM key, sealed for "key:<fingerprint>".
-- notified is the last expiry notice sent for the version: '', window,
-- urgent or expired.
CREATE TABLE certs_versions (
    id             INTEGER PRIMARY KEY,
    certificate_id INTEGER NOT NULL REFERENCES certs_certificates (id) ON DELETE CASCADE,
    origin         TEXT NOT NULL,
    names          TEXT NOT NULL,
    serial         TEXT NOT NULL,
    fingerprint    TEXT NOT NULL UNIQUE,
    issuer         TEXT NOT NULL,
    key_type       TEXT NOT NULL,
    not_before     INTEGER NOT NULL,
    not_after      INTEGER NOT NULL,
    chain          TEXT NOT NULL,
    sealed_key     BLOB NOT NULL,
    notified       TEXT NOT NULL,
    created_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX certs_versions_certificate ON certs_versions (certificate_id, created_at);

-- +goose Down
DROP TABLE certs_versions;
DROP TABLE certs_certificates;

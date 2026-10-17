-- +goose Up
-- A certificate whose source is 'provider' gets its versions from the
-- source provider instance source_provider, which proves control of the
-- domains with the solver instance solver_provider, if it needs one. The
-- instances live in the kernel's table, which modules do not reference;
-- certs refuses their deletion while a certificate uses them instead.
ALTER TABLE certs_certificates ADD COLUMN source_provider INTEGER;
ALTER TABLE certs_certificates ADD COLUMN solver_provider INTEGER;

-- An issuance is getting a new version from the source, tried until it
-- succeeds or fails for good (PLAN.md 4.3).
-- reason: first (the certificate was added), renewal or manual.
-- state: pending (the next attempt starts at next_at), running, done or
-- failed. A pending issuance with an error is waiting to retry.
-- error_key and error_params tell why the last attempt failed.
-- started_at is when the last attempt started.
CREATE TABLE certs_issuances (
    id             INTEGER PRIMARY KEY,
    certificate_id INTEGER NOT NULL REFERENCES certs_certificates (id) ON DELETE CASCADE,
    reason         TEXT NOT NULL,
    state          TEXT NOT NULL,
    attempts       INTEGER NOT NULL,
    next_at        INTEGER NOT NULL,
    version_id     INTEGER REFERENCES certs_versions (id) ON DELETE SET NULL,
    error_key      TEXT NOT NULL,
    error_params   TEXT NOT NULL,
    created_at     INTEGER NOT NULL,
    started_at     INTEGER,
    finished_at    INTEGER
) STRICT;

-- A certificate has at most one issuance under way.
CREATE UNIQUE INDEX certs_issuances_open ON certs_issuances (certificate_id) WHERE state IN ('pending', 'running');
CREATE INDEX certs_issuances_certificate ON certs_issuances (certificate_id, id);
CREATE INDEX certs_issuances_created ON certs_issuances (created_at);

-- The log of an issuance, line by line, across its attempts. key and
-- params are a message and its arguments (JSON); detail is raw text, such
-- as a CA's error.
CREATE TABLE certs_issuance_log (
    issuance_id INTEGER NOT NULL REFERENCES certs_issuances (id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    at          INTEGER NOT NULL,
    attempt     INTEGER NOT NULL,
    level       TEXT NOT NULL,
    step        TEXT NOT NULL,
    key         TEXT NOT NULL,
    params      TEXT NOT NULL,
    detail      TEXT NOT NULL,
    PRIMARY KEY (issuance_id, seq)
) STRICT, WITHOUT ROWID;

-- +goose Down
DROP TABLE certs_issuance_log;
DROP TABLE certs_issuances;
ALTER TABLE certs_certificates DROP COLUMN solver_provider;
ALTER TABLE certs_certificates DROP COLUMN source_provider;

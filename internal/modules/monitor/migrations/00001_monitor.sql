-- +goose Up
-- The certificates the monitor checks, as the certs module tells of them
-- (contracts.CertificateIssued and CertificateCurrent): the version in use,
-- since when, and when their domains are checked next. Times are Unix
-- milliseconds.
CREATE TABLE monitor_certificates (
    certificate_id INTEGER PRIMARY KEY,
    domains        TEXT NOT NULL, -- JSON array, the primary first
    version_id     INTEGER NOT NULL,
    fingerprint    TEXT NOT NULL, -- SHA-256 of the version's leaf, lowercase hex
    not_after      INTEGER NOT NULL,
    current_since  INTEGER NOT NULL,
    check_at       INTEGER NOT NULL
) STRICT;

CREATE INDEX monitor_certificates_due ON monitor_certificates (check_at);

-- What each domain served the last time the monitor connected to it.
-- alerted is the fingerprint of the version an alert went out for, so that
-- it goes out once.
CREATE TABLE monitor_results (
    certificate_id INTEGER NOT NULL REFERENCES monitor_certificates (certificate_id) ON DELETE CASCADE,
    domain         TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('ok', 'old', 'other', 'unreachable')),
    fingerprint    TEXT NOT NULL,
    not_after      INTEGER NOT NULL,
    issuer         TEXT NOT NULL,
    error          TEXT NOT NULL,
    checked_at     INTEGER NOT NULL,
    alerted        TEXT NOT NULL,
    PRIMARY KEY (certificate_id, domain)
) STRICT, WITHOUT ROWID;

-- +goose Down
DROP TABLE monitor_results;
DROP TABLE monitor_certificates;

-- +goose Up
-- The issuer of the version in use, as a domain served it: an older version
-- has it too, unlike a certificate of someone else's, such as a CDN's own.
ALTER TABLE monitor_certificates ADD COLUMN issuer TEXT NOT NULL DEFAULT '';

-- Results by view as well: '' when visitors from mainland China and from
-- elsewhere reach a domain the same way, 'cn' and 'global' when not.
CREATE TABLE monitor_results_views (
    certificate_id INTEGER NOT NULL REFERENCES monitor_certificates (certificate_id) ON DELETE CASCADE,
    domain         TEXT NOT NULL,
    view           TEXT NOT NULL CHECK (view IN ('', 'cn', 'global')),
    state          TEXT NOT NULL CHECK (state IN ('ok', 'old', 'other', 'unreachable')),
    fingerprint    TEXT NOT NULL,
    not_after      INTEGER NOT NULL,
    issuer         TEXT NOT NULL,
    error          TEXT NOT NULL,
    checked_at     INTEGER NOT NULL,
    alerted        TEXT NOT NULL,
    PRIMARY KEY (certificate_id, domain, view)
) STRICT, WITHOUT ROWID;

INSERT INTO monitor_results_views
SELECT certificate_id, domain, '', state, fingerprint, not_after, issuer, error, checked_at, alerted FROM monitor_results;

DROP TABLE monitor_results;
ALTER TABLE monitor_results_views RENAME TO monitor_results;

-- +goose Down
CREATE TABLE monitor_results_flat (
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

INSERT INTO monitor_results_flat
SELECT certificate_id, domain, state, fingerprint, not_after, issuer, error, checked_at, alerted FROM monitor_results WHERE view <> 'global';

DROP TABLE monitor_results;
ALTER TABLE monitor_results_flat RENAME TO monitor_results;
ALTER TABLE monitor_certificates DROP COLUMN issuer;

-- +goose Up
-- Sites whose certificates JEC watches but does not manage, each with what
-- it served when last checked. state is pending (not checked yet), ok,
-- expiring, expired, invalid (not trusted, or not for the host) or
-- unreachable. notified is the expiry level told for the certificate of
-- fingerprint: '', window, urgent or expired. Times are Unix milliseconds;
-- names is a JSON array.
CREATE TABLE watch_sites (
    id          INTEGER PRIMARY KEY,
    host        TEXT NOT NULL,
    port        INTEGER NOT NULL,
    name        TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    check_at    INTEGER NOT NULL,
    checked_at  INTEGER,
    state       TEXT NOT NULL,
    not_after   INTEGER,
    issuer      TEXT NOT NULL,
    names       TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    error       TEXT NOT NULL,
    notified    TEXT NOT NULL,
    UNIQUE (host, port)
) STRICT;

CREATE INDEX watch_sites_due ON watch_sites (check_at);

-- +goose Down
DROP TABLE watch_sites;

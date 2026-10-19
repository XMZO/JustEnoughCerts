-- +goose Up
-- Registered domains whose registration JEC watches through RDAP: those
-- the certificates and the sites are under, kept in step by the module,
-- and those added by hand (manual = 1). certificates and sites are JSON
-- arrays of the IDs under the domain. state is pending (not looked up
-- yet), ok, expiring, expired, unknown (the registry tells no end, or has
-- no RDAP) or failed. notified is the expiry level told for the end in
-- expires_at: '', window, urgent or expired. Times are Unix milliseconds.
CREATE TABLE watch_domains (
    name         TEXT PRIMARY KEY,
    manual       INTEGER NOT NULL,
    certificates TEXT NOT NULL,
    sites        TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    check_at     INTEGER NOT NULL,
    checked_at   INTEGER,
    state        TEXT NOT NULL,
    expires_at   INTEGER,
    registrar    TEXT NOT NULL,
    error        TEXT NOT NULL,
    notified     TEXT NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX watch_domains_due ON watch_domains (check_at);

-- +goose Down
DROP TABLE watch_domains;

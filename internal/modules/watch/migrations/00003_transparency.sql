-- +goose Up
-- Certificate transparency: the certificates logged for the registered
-- domains JEC holds certificates under, as Cert Spotter tells them, to tell
-- of one JEC does not hold.
--
-- watch_ct is the one row of settings: enabled, and the Cert Spotter API
-- key, sealed, if any.
CREATE TABLE watch_ct (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    enabled    INTEGER NOT NULL,
    api_key    BLOB,
    updated_at INTEGER NOT NULL
) STRICT;

-- watch_ct_domains are the domains searched: last_id is the ID of the last
-- certificate found, from which the next search goes on; scanned is 1 once
-- a search reached the end, as what the first finds is not told of.
CREATE TABLE watch_ct_domains (
    name       TEXT PRIMARY KEY,
    last_id    TEXT NOT NULL,
    scanned    INTEGER NOT NULL,
    check_at   INTEGER NOT NULL,
    checked_at INTEGER,
    error      TEXT NOT NULL
) STRICT, WITHOUT ROWID;

-- watch_ct_certificates are the certificates found, by Cert Spotter's ID.
-- names is the JSON array of the names, sorted; pubkey the SHA-256 of the
-- public key and sha256 that of the certificate, in hex. state is new (not
-- told yet, while JEC may still be storing its own), own (a certificate
-- JEC holds; certificate_id says which), baseline (found by the first
-- search, not told), unknown (told), trusted (names and issuer trusted)
-- or ignored. Times are Unix milliseconds.
CREATE TABLE watch_ct_certificates (
    id             TEXT PRIMARY KEY,
    domain         TEXT NOT NULL,
    names          TEXT NOT NULL,
    issuer         TEXT NOT NULL,
    issuer_dn      TEXT NOT NULL,
    pubkey         TEXT NOT NULL,
    sha256         TEXT NOT NULL,
    not_before     INTEGER NOT NULL,
    not_after      INTEGER NOT NULL,
    state          TEXT NOT NULL,
    certificate_id INTEGER NOT NULL,
    found_at       INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX watch_ct_certificates_domain ON watch_ct_certificates (domain);

-- watch_ct_trusted are the names and issuers whose certificates are made
-- elsewhere on purpose, as by a CDN: names is a JSON array as above.
CREATE TABLE watch_ct_trusted (
    names      TEXT NOT NULL,
    issuer     TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (names, issuer)
) STRICT, WITHOUT ROWID;

-- +goose Down
DROP TABLE watch_ct_trusted;
DROP TABLE watch_ct_certificates;
DROP TABLE watch_ct_domains;
DROP TABLE watch_ct;

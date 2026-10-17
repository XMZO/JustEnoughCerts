-- +goose Up
-- The registration behind each provider instance of type
-- namecheap.ssl_manager. token_digest is the SHA-256 of the one-time token
-- it was made with, in hexadecimal, so that a new token registers again.
-- sealed_access is the access token, sealed for "access:<provider_id>".
-- issued_at and expires_at are the access token's, 0 while unknown;
-- refresh_after holds back refreshing after a refusal that asked for it.
-- Times are Unix seconds.
CREATE TABLE namecheap_integrations (
    provider_id      INTEGER PRIMARY KEY,
    token_digest     TEXT NOT NULL,
    sealed_access    BLOB NOT NULL,
    integration_hash TEXT NOT NULL,
    issued_at        INTEGER NOT NULL,
    expires_at       INTEGER NOT NULL,
    refresh_after    INTEGER NOT NULL,
    registered_at    INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE namecheap_integrations;

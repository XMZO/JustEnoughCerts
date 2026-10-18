-- +goose Up
-- The single sign-on through an OpenID Connect provider: one row of
-- settings, the client secret sealed, and the sign-ins under way, by the
-- SHA-256 of their state, each with its PKCE verifier and nonce, sealed,
-- for ten minutes at most.
CREATE TABLE oidc_settings (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    enabled       INTEGER NOT NULL,
    label         TEXT NOT NULL,
    issuer        TEXT NOT NULL,
    client_id     TEXT NOT NULL,
    client_secret BLOB,
    allowed       TEXT NOT NULL,
    redirect_uri  TEXT NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE oidc_logins (
    state_hash BLOB PRIMARY KEY,
    sealed     BLOB NOT NULL,
    created_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE oidc_logins;
DROP TABLE oidc_settings;

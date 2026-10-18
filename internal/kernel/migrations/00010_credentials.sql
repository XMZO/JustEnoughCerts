-- +goose Up
-- Credentials (internal/kernel/providers): the keys of an account at a
-- vendor, such as a cloud's AccessKey, which several provider instances
-- share. kind names a kind of credential that the types taking it declare;
-- config and secrets are as kernel_providers', the secrets sealed for
-- "credential:<id>". used_at is when an instance last opened it, in Unix
-- milliseconds like the other times.
CREATE TABLE kernel_credentials (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL UNIQUE,
    config     TEXT NOT NULL,
    secrets    BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    used_at    INTEGER
) STRICT;

-- The credential of an instance fills the fields that its type takes from
-- one; a credential an instance uses cannot go.
ALTER TABLE kernel_providers ADD COLUMN credential_id INTEGER REFERENCES kernel_credentials (id);

CREATE INDEX kernel_providers_credential ON kernel_providers (credential_id);

-- +goose Down
DROP INDEX kernel_providers_credential;
ALTER TABLE kernel_providers DROP COLUMN credential_id;
DROP TABLE kernel_credentials;

-- +goose Up
-- Configured providers (internal/kernel/providers): an ACME account, a DNS
-- provider with its credentials, a deploy target. type names a type that a
-- module registers; config holds the plain settings as a JSON object, and
-- secrets the others, sealed for "provider:<id>". Times are Unix
-- milliseconds. AUTOINCREMENT: an ID is never reused, as modules keep
-- data of their own by instance ID, such as an ACME account's key.
CREATE TABLE kernel_providers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    type       TEXT NOT NULL,
    name       TEXT NOT NULL,
    config     TEXT NOT NULL,
    secrets    BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX kernel_providers_type ON kernel_providers (type);

-- +goose Down
DROP TABLE kernel_providers;

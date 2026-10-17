-- +goose Up
-- The ACME account behind each provider instance of type acme.account:
-- the directory it belongs to, its URL once registered ('' before), and
-- its key, PKCS #8 PEM sealed for "account:<provider_id>". Times are Unix
-- milliseconds.
CREATE TABLE acme_accounts (
    provider_id INTEGER PRIMARY KEY,
    directory   TEXT NOT NULL,
    location    TEXT NOT NULL,
    sealed_key  BLOB NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE acme_accounts;

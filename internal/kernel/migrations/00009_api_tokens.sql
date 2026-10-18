-- +goose Up
-- API tokens let programs use the API as their account, in place of the
-- session cookie: in the Authorization header, as bearer tokens. Like a
-- session, a token is known by the SHA-256 of its value only. scope is
-- read (GET and HEAD alone) or write; last_used_at is written at most once
-- a minute.
CREATE TABLE kernel_api_tokens (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES kernel_users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   BLOB NOT NULL UNIQUE,
    scope        TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_used_at TEXT
) STRICT;

CREATE INDEX kernel_api_tokens_user ON kernel_api_tokens (user_id);

-- +goose Down
DROP TABLE kernel_api_tokens;

-- +goose Up
CREATE TABLE kernel_users (
    id            INTEGER PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    locale        TEXT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
) STRICT;

-- A session is known by the SHA-256 of its cookie value only.
CREATE TABLE kernel_sessions (
    token_hash BLOB PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES kernel_users (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
) STRICT;

CREATE INDEX kernel_sessions_user ON kernel_sessions (user_id);

-- +goose Down
DROP TABLE kernel_sessions;
DROP TABLE kernel_users;

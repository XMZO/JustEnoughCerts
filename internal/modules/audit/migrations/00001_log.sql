-- +goose Up
-- What was done through the API: each operation that may change something,
-- with who asked, from where, and the status of the answer. user is ''
-- for none, as for a sign-in; a year of it is kept.
CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY,
    at         INTEGER NOT NULL,
    operation  TEXT NOT NULL,
    method     TEXT NOT NULL,
    path       TEXT NOT NULL,
    status     INTEGER NOT NULL,
    "user"     TEXT NOT NULL,
    client     TEXT NOT NULL,
    user_agent TEXT NOT NULL
) STRICT;

CREATE INDEX audit_log_at ON audit_log (at);

-- +goose Down
DROP TABLE audit_log;

-- +goose Up
-- The last run of each periodic job (internal/kernel/jobs), named
-- <module>.<job>. Times are Unix milliseconds; finished_at is NULL while a
-- run goes on, or when it was cut short.
CREATE TABLE kernel_jobs (
    name        TEXT PRIMARY KEY,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER,
    error       TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE kernel_jobs;

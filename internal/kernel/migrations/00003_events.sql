-- +goose Up
-- The event bus (internal/kernel/events). Times are Unix milliseconds.
CREATE TABLE kernel_events (
    id         INTEGER PRIMARY KEY,
    type       TEXT NOT NULL,
    source     TEXT NOT NULL,
    subject    TEXT NOT NULL,
    data       TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX kernel_events_created ON kernel_events (created_at);

-- One row per event and subscription that receives it.
CREATE TABLE kernel_deliveries (
    event_id   INTEGER NOT NULL REFERENCES kernel_events (id) ON DELETE CASCADE,
    subscriber TEXT NOT NULL,
    module     TEXT NOT NULL,
    state      TEXT NOT NULL CHECK (state IN ('pending', 'done', 'failed', 'expired')),
    attempts   INTEGER NOT NULL,
    next_at    INTEGER NOT NULL,
    last_error TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (event_id, subscriber)
) STRICT;

CREATE INDEX kernel_deliveries_due ON kernel_deliveries (subscriber, state, next_at, event_id);

-- +goose Down
DROP TABLE kernel_deliveries;
DROP TABLE kernel_events;

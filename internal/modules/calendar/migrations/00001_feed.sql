-- +goose Up
-- The key of the calendar feed: the one row there is while the feed is on.
-- Calendar apps subscribe to its URL, which they cannot sign in to, so the
-- key, random and unguessable, is all that guards it.
CREATE TABLE calendar_feed (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    key        TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE calendar_feed;

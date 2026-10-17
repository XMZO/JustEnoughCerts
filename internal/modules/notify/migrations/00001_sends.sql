-- +goose Up
-- What was sent through each notifier: a row for each event it was told
-- about, and one with event_id 0 for its latest test. state: sent,
-- retrying (tried again with the next delivery of the event) or failed
-- (for good). error_key and error_params tell why the last attempt failed.
CREATE TABLE notify_sends (
    event_id     INTEGER NOT NULL,
    provider_id  INTEGER NOT NULL,
    event        TEXT    NOT NULL,
    state        TEXT    NOT NULL,
    attempts     INTEGER NOT NULL,
    error_key    TEXT    NOT NULL DEFAULT '',
    error_params TEXT    NOT NULL DEFAULT '',
    at           INTEGER NOT NULL,
    PRIMARY KEY (event_id, provider_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX notify_sends_latest ON notify_sends (provider_id, at);

-- +goose Down
DROP TABLE notify_sends;

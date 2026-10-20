-- +goose Up
-- A note that came in the quiet hours of its notifier waits, held: state
-- held, the note as written, in JSON, and when to send it (Unix
-- milliseconds). Sent, note is emptied.
ALTER TABLE notify_sends ADD COLUMN note TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_sends ADD COLUMN send_at INTEGER NOT NULL DEFAULT 0;

CREATE INDEX notify_sends_held ON notify_sends (state, send_at);

-- +goose Down
DROP INDEX notify_sends_held;
ALTER TABLE notify_sends DROP COLUMN send_at;
ALTER TABLE notify_sends DROP COLUMN note;

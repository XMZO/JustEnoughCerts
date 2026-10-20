-- +goose Up
-- Where each sign-in came from, for the list of them: the client's address
-- and User-Agent header when it signed in, how (password, sso or setup),
-- and when it was last used, written at most every few minutes (RFC 3339,
-- NULL until then).
ALTER TABLE kernel_sessions ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE kernel_sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE kernel_sessions ADD COLUMN via TEXT NOT NULL DEFAULT '';
ALTER TABLE kernel_sessions ADD COLUMN last_seen TEXT;

-- +goose Down
ALTER TABLE kernel_sessions DROP COLUMN last_seen;
ALTER TABLE kernel_sessions DROP COLUMN via;
ALTER TABLE kernel_sessions DROP COLUMN user_agent;
ALTER TABLE kernel_sessions DROP COLUMN client;

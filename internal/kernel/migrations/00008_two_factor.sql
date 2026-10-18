-- +goose Up
-- Two-factor authentication: the sealed TOTP secret of an account that
-- turned it on, and the last time step whose code it used, so that a code
-- works once. And how many days a sign-in lasts.
ALTER TABLE kernel_users ADD COLUMN totp_secret BLOB;
ALTER TABLE kernel_users ADD COLUMN totp_step INTEGER NOT NULL DEFAULT 0;
ALTER TABLE kernel_users ADD COLUMN session_days INTEGER NOT NULL DEFAULT 7;

-- +goose Down
ALTER TABLE kernel_users DROP COLUMN session_days;
ALTER TABLE kernel_users DROP COLUMN totp_step;
ALTER TABLE kernel_users DROP COLUMN totp_secret;

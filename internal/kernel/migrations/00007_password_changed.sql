-- +goose Up
-- When each account last changed its password, which the settings page
-- shows. Accounts from before take the time of their last change of any
-- kind.
ALTER TABLE kernel_users ADD COLUMN password_changed_at TEXT;
UPDATE kernel_users SET password_changed_at = updated_at;

-- +goose Down
ALTER TABLE kernel_users DROP COLUMN password_changed_at;

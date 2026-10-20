-- +goose Up
-- notify_to is a JSON array of the notifiers the certificate's notes go
-- to; empty for all of them.
ALTER TABLE certs_certificates ADD COLUMN notify_to TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE certs_certificates DROP COLUMN notify_to;

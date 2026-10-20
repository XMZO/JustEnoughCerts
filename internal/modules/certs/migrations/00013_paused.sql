-- +goose Up
-- paused is 1 while the renewal of a certificate is paused: it is not
-- issued by itself, only when someone asks, and its end is still told.
ALTER TABLE certs_certificates ADD COLUMN paused INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE certs_certificates DROP COLUMN paused;

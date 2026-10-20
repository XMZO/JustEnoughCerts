-- +goose Up
-- When an API token stops working, RFC 3339; NULL for never.
ALTER TABLE kernel_api_tokens ADD COLUMN expires_at TEXT;

-- +goose Down
ALTER TABLE kernel_api_tokens DROP COLUMN expires_at;

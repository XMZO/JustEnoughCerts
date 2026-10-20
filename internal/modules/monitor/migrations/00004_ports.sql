-- +goose Up
-- ports are those the domains of a certificate are checked on, as
-- "443,993"; '' for 443 alone. On 25 and 587, TLS comes through SMTP's
-- STARTTLS.
ALTER TABLE monitor_certificates ADD COLUMN ports TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE monitor_certificates DROP COLUMN ports;

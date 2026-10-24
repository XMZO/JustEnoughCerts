-- +goose Up
-- unchecked are the domains of a certificate people turned the check off
-- for, as "intranet.example.com,old.example.com"; '' for none. They are
-- not connected to, and tell of nothing.
ALTER TABLE monitor_certificates ADD COLUMN unchecked TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE monitor_certificates DROP COLUMN unchecked;

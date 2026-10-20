-- +goose Up
-- A reminder someone set on a certificate, as to renew a subscription
-- bought: the day, as 2027-05-01 in the panel's time zone, '' for none;
-- what to be reminded of; and whether it was sent.
ALTER TABLE certs_certificates ADD COLUMN reminder_on TEXT NOT NULL DEFAULT '';
ALTER TABLE certs_certificates ADD COLUMN reminder_note TEXT NOT NULL DEFAULT '';
ALTER TABLE certs_certificates ADD COLUMN reminder_sent INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE certs_certificates DROP COLUMN reminder_sent;
ALTER TABLE certs_certificates DROP COLUMN reminder_note;
ALTER TABLE certs_certificates DROP COLUMN reminder_on;

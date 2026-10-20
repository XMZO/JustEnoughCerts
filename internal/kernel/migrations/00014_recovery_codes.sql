-- +goose Up
-- The recovery codes of an account with two-factor authentication on: a
-- JSON array of those not used yet, sealed by the master key. Each signs
-- in once in place of a code of the authenticator app.
ALTER TABLE kernel_users ADD COLUMN recovery_codes BLOB;

-- +goose Down
ALTER TABLE kernel_users DROP COLUMN recovery_codes;

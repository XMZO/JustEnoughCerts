-- +goose Up
-- deploy_window holds the hours, in the panel's time zone, a new version
-- of a certificate is deployed in, as "1-6"; '' for any time. A person who
-- asks deploys at once.
ALTER TABLE certs_certificates ADD COLUMN deploy_window TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE certs_certificates DROP COLUMN deploy_window;

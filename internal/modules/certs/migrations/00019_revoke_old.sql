-- +goose Up
-- Revoke the earlier versions once a new one is deployed to every target
-- (revoke.go); revoke_tried is when JEC last asked the CA to revoke a
-- version for it and failed, to ask again a day later.
ALTER TABLE certs_certificates ADD COLUMN revoke_old INTEGER NOT NULL DEFAULT 0;
ALTER TABLE certs_versions ADD COLUMN revoke_tried INTEGER;

-- +goose Down
ALTER TABLE certs_versions DROP COLUMN revoke_tried;
ALTER TABLE certs_certificates DROP COLUMN revoke_old;

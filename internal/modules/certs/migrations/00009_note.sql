-- +goose Up
-- note is what people wrote about the certificate, such as whose site it
-- is; '' for nothing.
ALTER TABLE certs_certificates ADD COLUMN note TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE certs_certificates DROP COLUMN note;

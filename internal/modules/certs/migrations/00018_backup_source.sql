-- +goose Up
-- The source that stands in for a certificate's own when that fails, as
-- an ACME account of another CA: a provider instance of a source type
-- marked Backup; NULL for none.
ALTER TABLE certs_certificates ADD COLUMN backup_provider INTEGER;

-- +goose Down
ALTER TABLE certs_certificates DROP COLUMN backup_provider;

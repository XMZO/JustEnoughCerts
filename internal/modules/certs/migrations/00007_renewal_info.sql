-- +goose Up
-- source_provider is the source that issued a version, for its renewal to
-- say which certificate it replaces (RFC 9773), and for asking that
-- source about renewing it; 0 for uploaded versions and for those issued
-- before JEC kept it. The renewal_ columns hold what the CA suggests
-- about renewing the version (ACME Renewal Information): the window, the
-- time JEC picked in it, and a page that explains it, all NULL or empty
-- until the CA answered; renewal_check is when to ask again.
ALTER TABLE certs_versions ADD COLUMN source_provider INTEGER NOT NULL DEFAULT 0;
ALTER TABLE certs_versions ADD COLUMN renewal_start INTEGER;
ALTER TABLE certs_versions ADD COLUMN renewal_end INTEGER;
ALTER TABLE certs_versions ADD COLUMN renewal_at INTEGER;
ALTER TABLE certs_versions ADD COLUMN renewal_explanation TEXT NOT NULL DEFAULT '';
ALTER TABLE certs_versions ADD COLUMN renewal_check INTEGER NOT NULL DEFAULT 0;
-- Versions issued before came from the source their certificate has now,
-- most likely. A wrong guess costs a question the CA cannot answer, and an
-- order whose replaces field it refuses, which the ACME source then
-- places again without it.
UPDATE certs_versions SET source_provider = coalesce((SELECT c.source_provider FROM certs_certificates c
    WHERE c.id = certs_versions.certificate_id AND c.source = 'provider'), 0)
    WHERE origin = 'issued';

-- +goose Down
ALTER TABLE certs_versions DROP COLUMN renewal_check;
ALTER TABLE certs_versions DROP COLUMN renewal_explanation;
ALTER TABLE certs_versions DROP COLUMN renewal_at;
ALTER TABLE certs_versions DROP COLUMN renewal_end;
ALTER TABLE certs_versions DROP COLUMN renewal_start;
ALTER TABLE certs_versions DROP COLUMN source_provider;

-- +goose Up
-- kind is where the backups go: s3, a bucket, or webdav, a folder of a
-- WebDAV server, at endpoint, with access_key and secret_key as the user
-- name and password.
ALTER TABLE offsite_settings ADD COLUMN kind TEXT NOT NULL DEFAULT 's3';

-- +goose Down
ALTER TABLE offsite_settings DROP COLUMN kind;

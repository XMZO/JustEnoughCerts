-- +goose Up
-- Where the daily backups are copied to: one row of settings for an S3
-- compatible bucket, the secret key sealed; and the backups copied there,
-- by file name. Times are Unix milliseconds.
CREATE TABLE offsite_settings (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    enabled    INTEGER NOT NULL,
    endpoint   TEXT NOT NULL,
    region     TEXT NOT NULL,
    bucket     TEXT NOT NULL,
    prefix     TEXT NOT NULL,
    path_style INTEGER NOT NULL,
    access_key TEXT NOT NULL,
    secret_key BLOB,
    updated_at INTEGER NOT NULL
);

CREATE TABLE offsite_uploads (
    name        TEXT PRIMARY KEY,
    size        INTEGER NOT NULL,
    uploaded_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE offsite_uploads;
DROP TABLE offsite_settings;

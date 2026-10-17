-- +goose Up
-- The files of domain validations JEC answers itself: what
-- http://<domain><path> returns, path being under /.well-known/. Times are
-- Unix milliseconds.
CREATE TABLE wellknown_files (
    domain     TEXT NOT NULL,
    path       TEXT NOT NULL,
    content    BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (domain, path)
) STRICT, WITHOUT ROWID;

CREATE INDEX wellknown_files_path ON wellknown_files (path, created_at);

-- +goose Down
DROP TABLE wellknown_files;

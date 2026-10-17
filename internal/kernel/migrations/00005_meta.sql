-- +goose Up
-- Facts about the installation itself, such as the value that tells at
-- startup whether the secret key is the one the database was sealed with.
CREATE TABLE kernel_meta (
    name  TEXT PRIMARY KEY,
    value BLOB NOT NULL
) STRICT;

-- +goose Down
DROP TABLE kernel_meta;

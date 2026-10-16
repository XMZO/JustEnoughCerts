-- +goose Up
-- Whether each module is switched on. A row stays when its module is
-- compiled out, so the choice comes back if the module does.
CREATE TABLE kernel_modules (
    id         TEXT PRIMARY KEY,
    enabled    INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    updated_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE kernel_modules;

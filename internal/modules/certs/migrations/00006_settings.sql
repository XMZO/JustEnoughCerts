-- +goose Up
-- The settings of the module: the defaults of new certificates and the time
-- of day renewals start. A setting not here has its default.
CREATE TABLE certs_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT, WITHOUT ROWID;

-- +goose Down
DROP TABLE certs_settings;

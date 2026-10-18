-- +goose Up
-- token names the API token an operation came with; '' for a session.
ALTER TABLE audit_log ADD COLUMN token TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE audit_log DROP COLUMN token;

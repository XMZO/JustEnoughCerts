-- +goose Up
-- incomplete is 1 when a site served its certificate without the
-- intermediates clients need: one that verifies once they are fetched as
-- the certificates name them (AIA).
ALTER TABLE watch_sites ADD COLUMN incomplete INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE watch_sites DROP COLUMN incomplete;

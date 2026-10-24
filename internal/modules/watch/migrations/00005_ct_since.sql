-- +goose Up
-- since is when the search was first switched on, in Unix milliseconds
-- (0 till then): a certificate issued before it was there before, and one
-- issued after is told of, though the first search of its domain finds it,
-- as for a domain JEC holds certificates under only later. Before, all the
-- first search of a domain found was taken as there before: since becomes
-- when the first certificate was found, and those issued after it are
-- judged again.
ALTER TABLE watch_ct ADD COLUMN since INTEGER NOT NULL DEFAULT 0;
UPDATE watch_ct SET since = COALESCE((SELECT MIN(found_at) FROM watch_ct_certificates), updated_at);
UPDATE watch_ct SET since = updated_at WHERE updated_at < since;
UPDATE watch_ct SET since = 0 WHERE enabled = 0 AND NOT EXISTS (SELECT 1 FROM watch_ct_domains);
UPDATE watch_ct_certificates SET state = 'new'
 WHERE state = 'baseline' AND not_before >= (SELECT since FROM watch_ct WHERE id = 1 AND since > 0);

-- +goose Down
ALTER TABLE watch_ct DROP COLUMN since;

-- +goose Up
-- cause is why the end of a registration is not known, when the state is
-- unknown: no_lookup (the registry has no RDAP or WHOIS to ask from here),
-- no_name (it has no such name), no_end (it tells no end) or no_answer
-- (none answered as it should); '' otherwise. The rows looked up before
-- have it from the words of their error.
ALTER TABLE watch_domains ADD COLUMN cause TEXT NOT NULL DEFAULT '';
UPDATE watch_domains SET cause = 'no_end'
 WHERE state = 'unknown' AND error LIKE 'the registry tells no expiration date%';
UPDATE watch_domains SET cause = 'no_lookup'
 WHERE state = 'unknown' AND (error LIKE 'the registry of % has no RDAP service%'
   OR error LIKE '%publishes no WHOIS or RDAP service%' OR error LIKE '%cannot be asked from here%');
UPDATE watch_domains SET cause = 'no_name'
 WHERE state = 'unknown' AND (error LIKE 'the registry has no such name%' OR error LIKE '%zone has no such name%');

-- +goose Down
ALTER TABLE watch_domains DROP COLUMN cause;

-- +goose Up
-- subject is what an operation acted on, as its handler named it: a
-- certificate's main domain, a provider's name; '' when none did.
ALTER TABLE audit_log ADD COLUMN subject TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE audit_log DROP COLUMN subject;

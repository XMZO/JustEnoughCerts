-- +goose Up
-- Why a deployment started: version (a new version came), target (the
-- target was added to the certificate) or manual (someone asked to deploy
-- now). Deployments from before all came with a version.
ALTER TABLE certs_deployments ADD COLUMN reason TEXT NOT NULL DEFAULT 'version';

-- +goose Down
ALTER TABLE certs_deployments DROP COLUMN reason;

-- +goose Up
-- The targets a certificate is installed at, in order: provider instances
-- of kind target, which certs refuses to see deleted while in use.
CREATE TABLE certs_targets (
    certificate_id INTEGER NOT NULL REFERENCES certs_certificates (id) ON DELETE CASCADE,
    provider_id    INTEGER NOT NULL,
    position       INTEGER NOT NULL,
    PRIMARY KEY (certificate_id, provider_id)
) STRICT, WITHOUT ROWID;

-- A deployment installs a version at a target, tried until it succeeds or
-- fails for good. state: pending (the next attempt starts at next_at; with
-- an error, a retry), running, done, failed or canceled (the target was
-- taken off the certificate). A newer version takes over a pending
-- deployment. ref is what the target holds afterwards, such as a
-- certificate ID at a CDN; removed_at is when a later deployment removed
-- it. error_key and error_params tell why the last attempt failed.
CREATE TABLE certs_deployments (
    id             INTEGER PRIMARY KEY,
    certificate_id INTEGER NOT NULL REFERENCES certs_certificates (id) ON DELETE CASCADE,
    version_id     INTEGER NOT NULL REFERENCES certs_versions (id) ON DELETE CASCADE,
    provider_id    INTEGER NOT NULL,
    state          TEXT NOT NULL,
    attempts       INTEGER NOT NULL,
    next_at        INTEGER NOT NULL,
    ref            TEXT NOT NULL,
    removed_at     INTEGER,
    error_key      TEXT NOT NULL,
    error_params   TEXT NOT NULL,
    created_at     INTEGER NOT NULL,
    started_at     INTEGER,
    finished_at    INTEGER
) STRICT;

-- A certificate has at most one deployment waiting per target.
CREATE UNIQUE INDEX certs_deployments_pending ON certs_deployments (certificate_id, provider_id) WHERE state = 'pending';
CREATE INDEX certs_deployments_target ON certs_deployments (certificate_id, provider_id, id);
CREATE INDEX certs_deployments_version ON certs_deployments (version_id);

-- The log of a deployment, as certs_issuance_log is of an issuance.
CREATE TABLE certs_deployment_log (
    deployment_id INTEGER NOT NULL REFERENCES certs_deployments (id) ON DELETE CASCADE,
    seq           INTEGER NOT NULL,
    at            INTEGER NOT NULL,
    attempt       INTEGER NOT NULL,
    level         TEXT NOT NULL,
    step          TEXT NOT NULL,
    key           TEXT NOT NULL,
    params        TEXT NOT NULL,
    detail        TEXT NOT NULL,
    PRIMARY KEY (deployment_id, seq)
) STRICT, WITHOUT ROWID;

-- +goose Down
DROP TABLE certs_deployment_log;
DROP TABLE certs_deployments;
DROP TABLE certs_targets;

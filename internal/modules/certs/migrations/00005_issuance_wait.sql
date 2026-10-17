-- +goose Up
-- An issuance whose source takes longer than an attempt, as a CA reviewing
-- an order, waits in state 'waiting': the source is asked again at
-- next_at, in the same attempt. An issuance keeps the private key of its
-- first attempt for the others, sealed_key, sealed for
-- "issuance-key:<id>", and what its source keeps between attempts,
-- sealed_state, sealed for "issuance-state:<id>". Both go once a version
-- is issued; a failed issuance keeps them, so that the next one can take
-- over an order the source still has.
ALTER TABLE certs_issuances ADD COLUMN sealed_key BLOB;
ALTER TABLE certs_issuances ADD COLUMN sealed_state BLOB;
DROP INDEX certs_issuances_open;
CREATE UNIQUE INDEX certs_issuances_open ON certs_issuances (certificate_id) WHERE state IN ('pending', 'running', 'waiting');

-- +goose Down
UPDATE certs_issuances SET state = 'pending' WHERE state = 'waiting';
DROP INDEX certs_issuances_open;
CREATE UNIQUE INDEX certs_issuances_open ON certs_issuances (certificate_id) WHERE state IN ('pending', 'running');
ALTER TABLE certs_issuances DROP COLUMN sealed_state;
ALTER TABLE certs_issuances DROP COLUMN sealed_key;

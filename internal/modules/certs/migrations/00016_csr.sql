-- +goose Up
-- A certificate request waiting for its certificate: JEC made the key,
-- the PKCS #8 PEM sealed for "csr:<certificate id>", and the CSR of it,
-- which a person hands to a CA; the certificate uploaded for it takes the
-- key. One for each certificate. created_at is Unix milliseconds.
CREATE TABLE certs_csr (
    certificate_id INTEGER PRIMARY KEY REFERENCES certs_certificates (id) ON DELETE CASCADE,
    sealed_key     BLOB NOT NULL,
    csr            TEXT NOT NULL,
    key_type       TEXT NOT NULL,
    created_at     INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE certs_csr;

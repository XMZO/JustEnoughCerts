package certs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// KeyBody is the public key of a version, as the API tells it.
type KeyBody struct {
	CertificateID int64  `json:"certificateId"`
	VersionID     int64  `json:"versionId"`
	SHA256        string `json:"sha256" doc:"SHA-256 of the version's SubjectPublicKeyInfo, lowercase hex, as certificate transparency search tells it"`
}

type keysOutput struct {
	Body struct {
		Keys []KeyBody `json:"keys"`
	}
}

func registerKeysAPI(a huma.API, s *service) {
	huma.Register(a, huma.Operation{
		OperationID: "certs-public-keys", Method: http.MethodGet, Path: "/public-keys", Tags: []string{"certificates"},
		Summary:     "List the public keys of every version of every certificate",
		Description: "For telling the certificates JEC holds from others for the same names, as found in certificate transparency logs.",
	}, func(ctx context.Context, _ *struct{}) (*keysOutput, error) {
		keys, err := s.publicKeys(ctx)
		if err != nil {
			return nil, err
		}
		out := &keysOutput{}
		out.Body.Keys = keys
		return out, nil
	})
}

// publicKeys returns the public key of each version; one whose chain does
// not parse is left out.
func (s *service) publicKeys(ctx context.Context) ([]KeyBody, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT certificate_id, id, chain FROM certs_versions ORDER BY id")
	if err != nil {
		return nil, err
	}
	out := []KeyBody{}
	for rows.Next() {
		var k KeyBody
		var chain string
		if err := rows.Scan(&k.CertificateID, &k.VersionID, &chain); err != nil {
			_ = rows.Close()
			return nil, err
		}
		certs, err := cryptox.ParseCertificates([]byte(chain))
		if err != nil || len(certs) == 0 {
			continue
		}
		sum := sha256.Sum256(certs[0].RawSubjectPublicKeyInfo)
		k.SHA256 = hex.EncodeToString(sum[:])
		out = append(out, k)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

package certs

import (
	"cmp"
	"context"
	"crypto"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// Certificate requests: for a certificate bought from a CA that JEC does
// not reach, as one activated by hand in a vendor's panel, JEC makes the
// private key and its CSR, which a person hands to the CA. The certificate
// the CA issues is uploaded without a key and takes this one; the key
// never leaves JEC. A certificate waits on one request at a time.

// errNotUploaded: a request is for a certificate uploaded by hand; one
// with a source renews by itself.
var errNotUploaded = errors.New("the certificate has a source")

func csrContext(id int64) string { return "csr:" + strconv.FormatInt(id, 10) }

// RequestBody is the request a certificate waits on.
type RequestBody struct {
	CSR       string    `json:"csr" doc:"The PEM certificate request, for the CA"`
	KeyType   string    `json:"keyType" enum:"ecdsa-p256,ecdsa-p384,rsa-2048,rsa-3072,rsa-4096"`
	CreatedAt time.Time `json:"createdAt"`
}

// newCSR makes a key of keyType and its CSR for domains, as the request
// certificate id waits on, in place of any before, in tx.
func (s *service) newCSR(ctx context.Context, tx *sql.Tx, id int64, domains []string, keyType string) error {
	key, err := cryptox.GenerateKey(keyType)
	if err != nil {
		return err
	}
	csr, err := cryptox.CSR(key, domains)
	if err != nil {
		return err
	}
	pemKey, err := cryptox.EncodeKey(key)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO certs_csr (certificate_id, sealed_key, csr, key_type, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (certificate_id) DO UPDATE SET sealed_key = excluded.sealed_key, csr = excluded.csr,
		 key_type = excluded.key_type, created_at = excluded.created_at`,
		id, s.secrets.Seal(pemKey, csrContext(id)), string(csr), keyType, s.now().UnixMilli())
	return err
}

// addRequested adds a certificate for domains, uploaded by hand, that waits
// for the certificate of a new request.
func (s *service) addRequested(ctx context.Context, domains []string, keyType string, renewBeforeDays int, targets []int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now()
	names, _ := json.Marshal(domains)
	var id int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO certs_certificates (domains, source, key_type, renew_before_days, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		string(names), sourceUpload, keyType, renewBeforeDays, now.UnixMilli(), now.UnixMilli()).Scan(&id)
	if err != nil {
		return 0, err
	}
	if err := s.setTargets(ctx, tx, &Certificate{ID: id}, targets); err != nil {
		return 0, err
	}
	if err := s.newCSR(ctx, tx, id, domains, keyType); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// request makes a new request for certificate id, uploaded by hand, for its
// domains, as for the renewal of a certificate bought.
func (s *service) request(ctx context.Context, id int64, keyType string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.load(ctx, tx, id)
	if err != nil {
		return err
	}
	if c.Source != sourceUpload {
		return errNotUploaded
	}
	if err := s.newCSR(ctx, tx, id, c.Domains, keyType); err != nil {
		return err
	}
	return tx.Commit()
}

// pendingCSR returns the request certificate id waits on, nil for none.
func (s *service) pendingCSR(ctx context.Context, id int64) (*RequestBody, error) {
	var b RequestBody
	var created int64
	err := s.db.QueryRowContext(ctx, "SELECT csr, key_type, created_at FROM certs_csr WHERE certificate_id = ?", id).
		Scan(&b.CSR, &b.KeyType, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.CreatedAt = time.UnixMilli(created).UTC()
	return &b, nil
}

// pendingKey returns the key of the request certificate id waits on, nil
// for none.
func (s *service) pendingKey(ctx context.Context, q queryRower, id int64) (crypto.Signer, error) {
	var sealed []byte
	err := q.QueryRowContext(ctx, "SELECT sealed_key FROM certs_csr WHERE certificate_id = ?", id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	data, err := s.secrets.Open(sealed, csrContext(id))
	if err != nil {
		return nil, fmt.Errorf("open the key of the request of certificate %d: %w", id, err)
	}
	return cryptox.ParseKey(data)
}

// queryRower is a *sql.DB or a *sql.Tx.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// settleCSR forgets the request of certificate id once a version with its
// key came, in tx.
func (s *service) settleCSR(ctx context.Context, tx *sql.Tx, id int64, key crypto.Signer) error {
	pending, err := s.pendingKey(ctx, tx, id)
	if err != nil || pending == nil {
		return err
	}
	type equaler interface{ Equal(crypto.PublicKey) bool }
	if p, ok := pending.Public().(equaler); ok && p.Equal(key.Public()) {
		_, err = tx.ExecContext(ctx, "DELETE FROM certs_csr WHERE certificate_id = ?", id)
	}
	return err
}

// keyFor reads an upload for certificate id that came without a key: the
// certificate takes the key of the request the certificate waits on, or
// of its version in use, as a CA's reissue for the same CSR does. err is
// the error of the upload as it came, kept when no key fits.
func (s *service) keyFor(ctx context.Context, id int64, f UploadFields, err error) (*cryptox.Bundle, error) {
	if !errors.Is(err, cryptox.ErrNoKey) || len(f.PFX) > 0 {
		return nil, err
	}
	certs, perr := cryptox.ParseCertificates([]byte(f.Certificate))
	if perr != nil {
		return nil, err
	}
	var keys []crypto.Signer
	if k, kerr := s.pendingKey(ctx, s.db, id); kerr != nil {
		return nil, kerr
	} else if k != nil {
		keys = append(keys, k)
	}
	if k, kerr := s.currentKey(ctx, id); kerr != nil {
		return nil, kerr
	} else if k != nil {
		keys = append(keys, k)
	}
	for _, k := range keys {
		if b, berr := cryptox.NewBundle(certs, k); berr == nil {
			return b, nil
		}
	}
	return nil, err
}

// requestFor finds the certificate whose request the certificates in pem
// were issued for, as an upload of a new certificate without a key may be:
// 0 when none was.
func (s *service) requestFor(ctx context.Context, pem string) (int64, *cryptox.Bundle, error) {
	certs, err := cryptox.ParseCertificates([]byte(pem))
	if err != nil {
		return 0, nil, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT certificate_id FROM certs_csr ORDER BY certificate_id")
	if err != nil {
		return 0, nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, nil, err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, nil, err
	}
	for _, id := range ids {
		k, err := s.pendingKey(ctx, s.db, id)
		if err != nil {
			return 0, nil, err
		}
		if k == nil {
			continue
		}
		if b, err := cryptox.NewBundle(certs, k); err == nil {
			return id, b, nil
		}
	}
	return 0, nil, nil
}

// currentKey returns the key of the version in use of certificate id, nil
// for none.
func (s *service) currentKey(ctx context.Context, id int64) (crypto.Signer, error) {
	var sealed []byte
	var fingerprint string
	err := s.db.QueryRowContext(ctx,
		`SELECT v.sealed_key, v.fingerprint FROM certs_certificates c JOIN certs_versions v ON v.id = c.current_version_id WHERE c.id = ?`, id).
		Scan(&sealed, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	data, err := s.secrets.Open(sealed, "key:"+fingerprint)
	if err != nil {
		return nil, err
	}
	return cryptox.ParseKey(data)
}

type requestInput struct {
	Body struct {
		Domains         []string `json:"domains" minItems:"1" maxItems:"100" doc:"The domains, the primary first; a wildcard as *.example.com"`
		KeyType         string   `json:"keyType,omitempty" enum:"ecdsa-p256,ecdsa-p384,rsa-2048,rsa-3072,rsa-4096" doc:"The type of the key; RSA 2048, which every CA takes, if left out"`
		RenewBeforeDays int      `json:"renewBeforeDays,omitempty" minimum:"1" maximum:"365" doc:"Days before the end to send a reminder; that of the settings if left out"`
		Targets         []int64  `json:"targets,omitempty" maxItems:"50" doc:"The target provider instances to deploy each version to, in order"`
	}
}

type csrInput struct {
	ID   int64 `path:"id"`
	Body struct {
		KeyType string `json:"keyType,omitempty" enum:"ecdsa-p256,ecdsa-p384,rsa-2048,rsa-3072,rsa-4096" doc:"The type of the new key; that of the certificate if left out"`
	}
}

type requestOutput struct{ Body RequestBody }

func registerCSRAPI(a huma.API, s *service) {
	huma.Register(a, huma.Operation{
		OperationID: "certs-request", Method: http.MethodPost, Path: "/certificates/request", DefaultStatus: http.StatusCreated,
		Summary: "Add a certificate to buy from a CA by hand, with a key and its CSR made by JEC",
		Description: "The certificate waits (state awaiting) until the one the CA issues for the CSR is uploaded, without a key: " +
			"it takes the key JEC made, which never leaves JEC.",
	}, func(ctx context.Context, in *requestInput) (*certificateOutput, error) {
		invalid := api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
		domains, bad := normalizeDomains(in.Body.Domains)
		for _, i := range bad {
			invalid = invalid.WithField(fmt.Sprintf("body.domains[%d]", i), i18n.CertsErrorBadDomain)
		}
		targets, badTargets := s.checkTargets(ctx, in.Body.Targets)
		for _, f := range badTargets {
			invalid = invalid.WithField(f.Location, f.Code)
		}
		keyType := cmp.Or(in.Body.KeyType, defaultKeyType)
		if !slices.Contains(cryptox.KeyTypes, keyType) {
			invalid = invalid.WithField("body.keyType", i18n.KernelErrorInvalidField)
		}
		if len(invalid.Fields) > 0 {
			return nil, invalid
		}
		renewBefore := in.Body.RenewBeforeDays
		if renewBefore == 0 {
			st, err := s.settings(ctx)
			if err != nil {
				return nil, err
			}
			renewBefore = st.RenewBeforeDays
		}
		id, err := s.addRequested(ctx, domains, keyType, renewBefore, targets)
		if err != nil {
			return nil, err
		}
		return s.output(ctx, id)
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-request-csr", Method: http.MethodPost, Path: "/certificates/{id}/csr",
		Summary:     "Make a new key and CSR for a certificate uploaded by hand, as to renew one bought",
		Description: "In place of any request it waited on. The certificate the CA issues for it is uploaded without a key.",
	}, func(ctx context.Context, in *csrInput) (*requestOutput, error) {
		s.named(ctx, in.ID)
		keyType := in.Body.KeyType
		if keyType == "" {
			c, err := s.load(ctx, s.db, in.ID)
			if err != nil {
				return nil, serviceError(err)
			}
			keyType = c.KeyType
		}
		if !slices.Contains(cryptox.KeyTypes, keyType) {
			keyType = defaultKeyType
		}
		switch err := s.request(ctx, in.ID, keyType); {
		case errors.Is(err, errNotUploaded):
			return nil, api.NewError(http.StatusConflict, i18n.CertsErrorCsrSource, nil)
		case err != nil:
			return nil, serviceError(err)
		}
		b, err := s.pendingCSR(ctx, in.ID)
		if err != nil || b == nil {
			return nil, errors.Join(err, errNotFound)
		}
		return &requestOutput{Body: *b}, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-drop-csr", Method: http.MethodDelete, Path: "/certificates/{id}/csr", DefaultStatus: http.StatusNoContent,
		Summary: "Forget the request a certificate waits on, with its key",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		s.named(ctx, in.ID)
		res, err := s.db.ExecContext(ctx, "DELETE FROM certs_csr WHERE certificate_id = ?", in.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		}
		return nil, nil
	})
}

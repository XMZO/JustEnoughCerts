package certs

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// download returns version of certificate id as a ZIP of PEM files, laid
// out as certbot does, and a file name for it. It publishes
// certificate.key_downloaded, for the audit trail.
func (s *service) download(ctx context.Context, id, version int64, username string) ([]byte, string, error) {
	var chain, fingerprint, names string
	var sealed []byte
	var notAfter int64
	err := s.db.QueryRowContext(ctx,
		"SELECT chain, sealed_key, fingerprint, names, not_after FROM certs_versions WHERE id = ? AND certificate_id = ?",
		version, id).Scan(&chain, &sealed, &fingerprint, &names, &notAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", errNotFound
	}
	if err != nil {
		return nil, "", err
	}
	key, err := s.secrets.Open(sealed, "key:"+fingerprint)
	if err != nil {
		return nil, "", fmt.Errorf("open the private key of version %d: %w", version, err)
	}
	certs, err := cryptox.ParseCertificates([]byte(chain))
	if err != nil {
		return nil, "", err
	}
	var domains []string
	if err := json.Unmarshal([]byte(names), &domains); err != nil {
		return nil, "", err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	now := s.now()
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"cert.pem", cryptox.EncodeCertificates(certs[0])},
		{"chain.pem", cryptox.EncodeCertificates(certs[1:]...)},
		{"fullchain.pem", []byte(chain)},
		{"privkey.pem", key},
	} {
		if len(f.data) == 0 {
			continue // no intermediates
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return nil, "", err
		}
		if _, err := w.Write(f.data); err != nil {
			return nil, "", err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}

	err = s.events.Publish(ctx, contracts.CertificateKeyDownloaded, strconv.FormatInt(id, 10), contracts.CertificateEvent{
		CertificateID: id, Domains: domains, VersionID: version, NotAfter: time.UnixMilli(notAfter).UTC(), Username: username,
	})
	if err != nil {
		return nil, "", err
	}
	s.log.Info("private key downloaded", "certificate", id, "version", version, "username", username)
	return buf.Bytes(), zipName(domains, time.UnixMilli(notAfter)), nil
}

// csr makes a certificate request of the private key of version of
// certificate id, for the version's names.
func (s *service) csr(ctx context.Context, id, version int64) ([]byte, error) {
	var sealed []byte
	var fingerprint, names string
	err := s.db.QueryRowContext(ctx, "SELECT sealed_key, fingerprint, names FROM certs_versions WHERE id = ? AND certificate_id = ?",
		version, id).Scan(&sealed, &fingerprint, &names)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	data, err := s.secrets.Open(sealed, "key:"+fingerprint)
	if err != nil {
		return nil, fmt.Errorf("open the private key of version %d: %w", version, err)
	}
	key, err := cryptox.ParseKey(data)
	if err != nil {
		return nil, err
	}
	var domains []string
	if err := json.Unmarshal([]byte(names), &domains); err != nil {
		return nil, err
	}
	return cryptox.CSR(key, domains)
}

// zipName names a download after its primary domain and end date, safe
// for any file system: *.example.com_2027-01-05.zip becomes
// star.example.com_2027-01-05.zip.
func zipName(domains []string, notAfter time.Time) string {
	name := "certificate"
	if len(domains) > 0 {
		name = strings.NewReplacer("*", "star", "/", "_", "\\", "_", ":", "_").Replace(domains[0])
	}
	return name + "_" + notAfter.UTC().Format(time.DateOnly) + ".zip"
}

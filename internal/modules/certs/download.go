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

// pfxOptions ask for a PKCS #12 file beside the PEM files.
type pfxOptions struct {
	password string // "" for none
	legacy   bool   // 3DES rather than AES-256
}

// zipFile is a file of a download.
type zipFile struct {
	name string
	data []byte
}

// versionFiles returns the files of version of certificate id, laid out as
// certbot does, with certificate.pfx when pfx is not nil, and the
// version's names and end.
func (s *service) versionFiles(ctx context.Context, id, version int64, pfx *pfxOptions) ([]zipFile, []string, time.Time, error) {
	var chain, fingerprint, names string
	var sealed []byte
	var notAfter int64
	err := s.db.QueryRowContext(ctx,
		"SELECT chain, sealed_key, fingerprint, names, not_after FROM certs_versions WHERE id = ? AND certificate_id = ?",
		version, id).Scan(&chain, &sealed, &fingerprint, &names, &notAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, time.Time{}, errNotFound
	}
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	key, err := s.secrets.Open(sealed, "key:"+fingerprint)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("open the private key of version %d: %w", version, err)
	}
	certs, err := cryptox.ParseCertificates([]byte(chain))
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	var domains []string
	if err := json.Unmarshal([]byte(names), &domains); err != nil {
		return nil, nil, time.Time{}, err
	}
	files := []zipFile{
		{"cert.pem", cryptox.EncodeCertificates(certs[0])},
		{"chain.pem", cryptox.EncodeCertificates(certs[1:]...)},
		{"fullchain.pem", []byte(chain)},
		{"privkey.pem", key},
	}
	if pfx != nil {
		signer, err := cryptox.ParseKey(key)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		data, err := cryptox.EncodePFX(certs, signer, pfx.password, pfx.legacy)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		files = append(files, zipFile{"certificate.pfx", data})
	}
	return files, domains, time.UnixMilli(notAfter), nil
}

// addZip writes files into zw under dir, leaving out empty ones, as
// chain.pem without intermediates.
func addZip(zw *zip.Writer, dir string, files []zipFile, at time.Time) error {
	for _, f := range files {
		if len(f.data) == 0 {
			continue
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: dir + f.name, Method: zip.Deflate, Modified: at})
		if err != nil {
			return err
		}
		if _, err := w.Write(f.data); err != nil {
			return err
		}
	}
	return nil
}

// downloaded publishes certificate.key_downloaded for version of
// certificate id, for the audit trail.
func (s *service) downloaded(ctx context.Context, id, version int64, domains []string, notAfter time.Time, username string) error {
	err := s.events.Publish(ctx, contracts.CertificateKeyDownloaded, strconv.FormatInt(id, 10), contracts.CertificateEvent{
		CertificateID: id, Domains: domains, VersionID: version, NotAfter: notAfter.UTC(), Username: username,
	})
	if err != nil {
		return err
	}
	s.log.Info("private key downloaded", "certificate", id, "version", version, "username", username)
	return nil
}

// download returns version of certificate id as a ZIP of its files
// (versionFiles), and a file name for it.
func (s *service) download(ctx context.Context, id, version int64, username string, pfx *pfxOptions) ([]byte, string, error) {
	files, domains, notAfter, err := s.versionFiles(ctx, id, version, pfx)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := addZip(zw, "", files, s.now()); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	if err := s.downloaded(ctx, id, version, domains, notAfter, username); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), zipName(domains, notAfter), nil
}

// exportAll returns the current version of every certificate as one ZIP,
// a folder each, named as a download of it is.
func (s *service) exportAll(ctx context.Context, username string) ([]byte, error) {
	certs, err := s.list(ctx)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	now := s.now()
	used := map[string]bool{}
	for _, c := range certs {
		if c.Current == nil {
			continue
		}
		files, domains, notAfter, err := s.versionFiles(ctx, c.ID, c.Current.ID, nil)
		if err != nil {
			return nil, err
		}
		dir := strings.TrimSuffix(zipName(domains, notAfter), ".zip")
		if used[dir] {
			dir += "_" + strconv.FormatInt(c.ID, 10)
		}
		used[dir] = true
		if err := addZip(zw, dir+"/", files, now); err != nil {
			return nil, err
		}
		if err := s.downloaded(ctx, c.ID, c.Current.ID, domains, notAfter, username); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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

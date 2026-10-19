package certs

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ocsp"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// How often JEC asks whether the CA revoked a current version: twice a
// day, as CAs revoke at a day's notice; an hour after the CA could not
// tell.
const (
	statusEvery = 12 * time.Hour
	statusRetry = time.Hour
	maxOCSP     = 64 << 10 // bytes of an OCSP answer read
	maxCRL      = 32 << 20 // bytes of a CRL read
	maxCRLs     = 64       // CRLs kept until their next update
	crlFallback = 6 * time.Hour
)

// revocation is what a CA tells of a certificate it revoked.
type revocation struct {
	at     time.Time
	reason int // RFC 5280
}

// statusChecker asks CAs whether they revoked certificates: through OCSP
// where the certificate names a responder, else through its CRL, which it
// keeps until the CRL's next update.
type statusChecker struct {
	client *http.Client
	now    func() time.Time

	mu   sync.Mutex
	crls map[string]*x509.RevocationList
}

// check asks the CA of leaf, which issuer signed, whether it revoked leaf:
// nil when it did not. It fails when the CA could not tell.
func (c *statusChecker) check(ctx context.Context, leaf, issuer *x509.Certificate) (*revocation, error) {
	var errs []error
	for _, url := range leaf.OCSPServer {
		r, err := c.ocsp(ctx, url, leaf, issuer)
		if err == nil {
			return r, nil
		}
		errs = append(errs, err)
	}
	for _, url := range leaf.CRLDistributionPoints {
		r, err := c.crl(ctx, url, leaf, issuer)
		if err == nil {
			return r, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, errNoStatus
	}
	return nil, errors.Join(errs...)
}

// errNoStatus: the certificate names neither an OCSP responder nor a CRL.
var errNoStatus = errors.New("the certificate tells no way to learn whether it was revoked")

func (c *statusChecker) ocsp(ctx context.Context, url string, leaf, issuer *x509.Certificate) (*revocation, error) {
	req, err := ocsp.CreateRequest(leaf, issuer, nil)
	if err != nil {
		return nil, err
	}
	body, err := c.fetch(ctx, http.MethodPost, url, req, "application/ocsp-request", maxOCSP)
	if err != nil {
		return nil, err
	}
	res, err := ocsp.ParseResponseForCert(body, leaf, issuer)
	if err != nil {
		return nil, fmt.Errorf("OCSP %s: %w", url, err)
	}
	switch res.Status {
	case ocsp.Good:
		return nil, nil
	case ocsp.Revoked:
		return &revocation{at: res.RevokedAt.UTC(), reason: res.RevocationReason}, nil
	}
	return nil, fmt.Errorf("OCSP %s: the responder does not know the certificate", url)
}

func (c *statusChecker) crl(ctx context.Context, url string, leaf, issuer *x509.Certificate) (*revocation, error) {
	now := c.now()
	c.mu.Lock()
	list := c.crls[url]
	c.mu.Unlock()
	if list == nil || !now.Before(c.until(list)) {
		body, err := c.fetch(ctx, http.MethodGet, url, nil, "", maxCRL)
		if err != nil {
			return nil, err
		}
		if list, err = x509.ParseRevocationList(body); err != nil {
			return nil, fmt.Errorf("CRL %s: %w", url, err)
		}
		c.mu.Lock()
		if c.crls == nil || len(c.crls) >= maxCRLs {
			c.crls = map[string]*x509.RevocationList{}
		}
		c.crls[url] = list
		c.mu.Unlock()
	}
	// Kept or new, it counts only when the issuer of the leaf signed it.
	if err := list.CheckSignatureFrom(issuer); err != nil {
		return nil, fmt.Errorf("CRL %s: %w", url, err)
	}
	for _, e := range list.RevokedCertificateEntries {
		if e.SerialNumber.Cmp(leaf.SerialNumber) == 0 {
			return &revocation{at: e.RevocationTime.UTC(), reason: e.ReasonCode}, nil
		}
	}
	return nil, nil
}

// until is when a CRL kept is to be fetched again.
func (c *statusChecker) until(list *x509.RevocationList) time.Time {
	if list.NextUpdate.IsZero() {
		return list.ThisUpdate.Add(crlFallback)
	}
	return list.NextUpdate
}

func (c *statusChecker) fetch(ctx context.Context, method, url string, body []byte, contentType string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered HTTP %d", req.URL.Host, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s answered more than %d bytes", req.URL.Host, limit)
	}
	return data, nil
}

// checkRevocation is the job certs.revocation. For each certificate whose
// current version comes with the certificate of its issuer, it asks the
// CA, when due, whether it revoked the version. A revocation is told
// (contracts.CertificateRevoked) and makes the certificate due for renewal
// at once.
func (s *service) checkRevocation(ctx context.Context) error {
	if s.status == nil {
		return nil
	}
	certs, err := s.list(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, c := range certs {
		v := c.Current
		if v == nil || !v.CARevokedAt.IsZero() || !v.RevokedAt.IsZero() || now.Before(v.StatusCheck) || !now.Before(v.NotAfter) {
			continue
		}
		next := now.Add(statusEvery)
		chain, err := cryptox.ParseCertificates([]byte(v.Chain))
		var r *revocation
		if err == nil && len(chain) > 1 {
			if r, err = s.status.check(ctx, chain[0], chain[1]); err != nil {
				s.log.Debug("revocation status", "certificate", c.ID, "err", err)
				next = now.Add(statusRetry)
				if errors.Is(err, errNoStatus) {
					next = now.Add(statusEvery)
				}
			}
		}
		if r == nil {
			if _, err := s.db.ExecContext(ctx, "UPDATE certs_versions SET status_check = ? WHERE id = ?", next.UnixMilli(), v.ID); err != nil {
				return err
			}
			continue
		}
		if err := s.revokedByCA(ctx, c, r); err != nil {
			return err
		}
	}
	return nil
}

// revokedByCA stores that the CA revoked the current version of c, and
// tells of it.
func (s *service) revokedByCA(ctx context.Context, c *Certificate, r *revocation) error {
	v := c.Current
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "UPDATE certs_versions SET ca_revoked_at = ?, ca_revoke_reason = ? WHERE id = ?",
		r.at.UnixMilli(), r.reason, v.ID); err != nil {
		return err
	}
	err = s.events.PublishTx(ctx, tx, contracts.CertificateRevoked, strconv.FormatInt(c.ID, 10), contracts.CertificateEvent{
		CertificateID: c.ID, Domains: c.Domains, VersionID: v.ID, Fingerprint: v.Fingerprint, NotAfter: v.NotAfter,
		Revocation: &contracts.Revocation{At: r.at, Reason: r.reason, Renewing: c.Source == sourceProvider},
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.log.Warn("the CA revoked a certificate", "certificate", c.ID, "version", v.ID, "reason", r.reason)
	return nil
}

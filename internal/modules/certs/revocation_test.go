package certs

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ocsp"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// statusCA answers OCSP requests and serves a CRL for the leaves of a test
// CA, from the serial numbers it is told were revoked.
type statusCA struct {
	t   *testing.T
	ca  *cryptoxtest.CA
	now func() time.Time

	mu      sync.Mutex
	revoked map[string]x509.RevocationListEntry
	asked   map[string]int // requests by path
}

func newStatusCA(t *testing.T, ca *cryptoxtest.CA, now func() time.Time) (*statusCA, *httptest.Server) {
	s := &statusCA{t: t, ca: ca, now: now, revoked: map[string]x509.RevocationListEntry{}, asked: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *statusCA) revoke(serial *big.Int, reason int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[serial.String()] = x509.RevocationListEntry{SerialNumber: serial, RevocationTime: s.now().Add(-time.Hour).Truncate(time.Second), ReasonCode: reason}
}

func (s *statusCA) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked[path]
}

func (s *statusCA) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked[r.URL.Path]++
	now := s.now()
	switch r.URL.Path {
	case "/ocsp":
		body, _ := io.ReadAll(r.Body)
		req, err := ocsp.ParseRequest(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		tmpl := ocsp.Response{Status: ocsp.Good, SerialNumber: req.SerialNumber, ThisUpdate: now.Add(-time.Hour), NextUpdate: now.Add(24 * time.Hour)}
		if e, ok := s.revoked[req.SerialNumber.String()]; ok {
			tmpl.Status, tmpl.RevokedAt, tmpl.RevocationReason = ocsp.Revoked, e.RevocationTime, e.ReasonCode
		}
		der, err := ocsp.CreateResponse(s.ca.Intermediate, s.ca.Intermediate, tmpl, s.ca.Signer())
		if err != nil {
			s.t.Error(err)
		}
		_, _ = w.Write(der)
	case "/crl":
		tmpl := &x509.RevocationList{Number: big.NewInt(now.Unix()), ThisUpdate: now.Add(-time.Hour), NextUpdate: now.Add(6 * time.Hour)}
		for _, e := range s.revoked {
			tmpl.RevokedCertificateEntries = append(tmpl.RevokedCertificateEntries, e)
		}
		der, err := x509.CreateRevocationList(rand.Reader, tmpl, s.ca.Intermediate, s.ca.Signer())
		if err != nil {
			s.t.Error(err)
		}
		_, _ = w.Write(der)
	default:
		http.NotFound(w, r)
	}
}

// leafAt issues a leaf for names that ends after days, which tells where
// to learn whether it was revoked.
func leafAt(t *testing.T, ca *cryptoxtest.CA, now time.Time, days int, ocspURL, crlURL string, names ...string) (*x509.Certificate, crypto.Signer) {
	key := cryptoxtest.NewKey(t)
	tmpl := &x509.Certificate{
		DNSNames: names, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Duration(days) * day),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ocspURL != "" {
		tmpl.OCSPServer = []string{ocspURL}
	}
	if crlURL != "" {
		tmpl.CRLDistributionPoints = []string{crlURL}
	}
	tmpl.Subject.CommonName = names[0]
	return ca.Sign(t, key, tmpl), key
}

func TestStatusChecker(t *testing.T) {
	ca := cryptoxtest.NewCA(t)
	now := start
	sca, srv := newStatusCA(t, ca, func() time.Time { return now })
	c := &statusChecker{client: srv.Client(), now: func() time.Time { return now }}

	// Through OCSP: good, then revoked.
	leaf, _ := leafAt(t, ca, now, 90, srv.URL+"/ocsp", srv.URL+"/crl", "ocsp.example.com")
	if r, err := c.check(t.Context(), leaf, ca.Intermediate); err != nil || r != nil {
		t.Fatalf("good = %+v %v", r, err)
	}
	sca.revoke(leaf.SerialNumber, 1)
	r, err := c.check(t.Context(), leaf, ca.Intermediate)
	if err != nil || r == nil || r.reason != 1 || !r.at.Equal(now.Add(-time.Hour)) {
		t.Fatalf("revoked = %+v %v", r, err)
	}
	if sca.count("/crl") != 0 {
		t.Error("the CRL was fetched while OCSP answered")
	}

	// Through the CRL, kept until its next update.
	leaf, _ = leafAt(t, ca, now, 90, "", srv.URL+"/crl", "crl.example.com")
	if r, err := c.check(t.Context(), leaf, ca.Intermediate); err != nil || r != nil {
		t.Fatalf("good = %+v %v", r, err)
	}
	sca.revoke(leaf.SerialNumber, 4)
	if r, _ := c.check(t.Context(), leaf, ca.Intermediate); r != nil || sca.count("/crl") != 1 {
		t.Fatalf("the CRL was not kept: %+v, %d fetches", r, sca.count("/crl"))
	}
	now = now.Add(7 * time.Hour)
	if r, err := c.check(t.Context(), leaf, ca.Intermediate); err != nil || r == nil || r.reason != 4 || sca.count("/crl") != 2 {
		t.Fatalf("revoked = %+v %v, %d fetches", r, err, sca.count("/crl"))
	}

	// A CRL signed by another CA is refused; a leaf that tells nothing
	// cannot be checked.
	other := cryptoxtest.NewCA(t)
	if _, err := c.check(t.Context(), leaf, other.Intermediate); err == nil {
		t.Error("took a CRL another CA signed")
	}
	plain, _ := leafAt(t, ca, now, 90, "", "", "plain.example.com")
	if _, err := c.check(t.Context(), plain, ca.Intermediate); !errors.Is(err, errNoStatus) {
		t.Errorf("no status = %v", err)
	}
}

func TestRevokedByCA(t *testing.T) {
	f := newFixture(t)
	sca, srv := newStatusCA(t, f.ca, func() time.Time { return f.now })
	f.svc.status = &statusChecker{client: srv.Client(), now: func() time.Time { return f.now }}
	leaf, key := leafAt(t, f.ca, f.now, 90, "", srv.URL+"/crl", "revoked.example.com")
	c := f.add(upload{cryptoxtest.CertPEM(leaf, f.ca.Intermediate), cryptoxtest.KeyPEM(t, key), key})
	plain := f.add(f.leaf(90, "plain.example.com"))

	if err := f.svc.checkRevocation(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.certificate(c.ID); got.State != stateOK || got.Current.CARevokedAt != nil {
		t.Fatalf("before = %+v", got)
	}
	// Not asked again before it is due.
	f.now = f.now.Add(time.Hour)
	sca.revoke(leaf.SerialNumber, 1)
	if err := f.svc.checkRevocation(t.Context()); err != nil || sca.count("/crl") != 1 {
		t.Fatalf("asked again: %v, %d fetches", err, sca.count("/crl"))
	}
	f.now = f.now.Add(statusEvery)
	if err := f.svc.checkRevocation(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := f.certificate(c.ID)
	if got.State != stateRevoked || got.Current.CARevokedAt == nil || got.Current.CARevokeReason != "key_compromise" {
		t.Fatalf("after = %s %+v", got.State, got.Current)
	}
	if p := f.certificate(plain.ID); p.State != stateOK {
		t.Errorf("the other certificate is %s", p.State)
	}
	var told []contracts.CertificateEvent
	for _, e := range f.events() {
		if e.typ == contracts.CertificateRevoked {
			told = append(told, e.data)
		}
	}
	if len(told) != 1 || told[0].CertificateID != c.ID || told[0].Revocation == nil || told[0].Revocation.Reason != 1 || told[0].Revocation.Renewing {
		t.Fatalf("told %+v", told)
	}
	// Told once: a revoked version is not asked about again.
	f.now = f.now.Add(statusEvery)
	if err := f.svc.checkRevocation(t.Context()); err != nil || sca.count("/crl") != 2 {
		t.Fatalf("asked about a revoked version: %v, %d fetches", err, sca.count("/crl"))
	}
}

func TestRevokedRenews(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.run()
	loaded, err := f.svc.load(t.Context(), f.db, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if is := f.certificate(c.ID).Issuance; is.Reason == "renewal" {
		t.Fatal("renewed before the revocation")
	}
	if err := f.svc.revokedByCA(t.Context(), loaded, &revocation{at: f.now.Add(-time.Hour), reason: 5}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := f.certificate(c.ID)
	if got.State != stateIssuing || got.Issuance.Reason != "renewal" || got.Current.CARevokeReason != "cessation_of_operation" {
		t.Fatalf("after = %s %+v %+v", got.State, got.Issuance, got.Current)
	}
	var told []contracts.CertificateEvent
	for _, e := range f.events() {
		if e.typ == contracts.CertificateRevoked {
			told = append(told, e.data)
		}
	}
	if len(told) != 1 || told[0].Revocation == nil || !told[0].Revocation.Renewing {
		t.Fatalf("told %+v", told)
	}
	// The new version is not revoked.
	f.run()
	if got := f.certificate(c.ID); got.State != stateOK || got.Current.CARevokedAt != nil {
		t.Errorf("renewed = %s %+v", got.State, got.Current)
	}
}

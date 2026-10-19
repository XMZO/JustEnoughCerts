package privateca

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// Bounds of a certificate's validity, in days. 825 is the most that Apple's
// devices take from a CA that users added.
const (
	defaultDays = 90
	maxDays     = 825
)

func caType() providers.Type {
	return providers.Type{
		ID: "privateca.ca", Kind: providers.Source, Name: i18n.PrivatecaCaName, Description: i18n.PrivatecaCaDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "certificate", Label: i18n.PrivatecaFieldCertificate, Help: i18n.PrivatecaFieldCertificateHelp, Kind: providers.Multiline, Required: true},
			{Key: "key", Label: i18n.PrivatecaFieldKey, Help: i18n.PrivatecaFieldKeyHelp, Kind: providers.Multiline, Secret: true, Required: true},
			{Key: "days", Label: i18n.PrivatecaFieldDays, Help: i18n.PrivatecaFieldDaysHelp, Kind: providers.Number, Default: strconv.Itoa(defaultDays)},
		},
	}
}

// ca signs certificates with the CA's key.
type ca struct {
	cert  *x509.Certificate
	chain []*x509.Certificate // above the leaf: the CA and those above it, but for a root
	key   crypto.Signer
	days  int
	now   func() time.Time
}

var (
	_ contracts.Source = (*ca)(nil)
	_ contracts.Tester = (*ca)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	days := defaultDays
	if s := strings.TrimSpace(in.Config["days"]); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxDays {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.PrivatecaErrorDays, Params: map[string]any{"max": maxDays}})
		}
		days = n
	}
	certs, err := cryptox.ParseCertificates([]byte(in.Value("certificate")))
	if err != nil || len(certs) == 0 {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.PrivatecaErrorCertificate, Err: err})
	}
	key, err := cryptox.ParseKey([]byte(in.Value("key")))
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.PrivatecaErrorKey, Err: err})
	}
	// The CA is the certificate of the key, wherever it is in the list.
	c := &ca{key: key, days: days, now: time.Now}
	type equaler interface{ Equal(crypto.PublicKey) bool }
	pub, ok := key.Public().(equaler)
	for _, cert := range certs {
		if ok && pub.Equal(cert.PublicKey) {
			c.cert = cert
			break
		}
	}
	if c.cert == nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.PrivatecaErrorMismatch})
	}
	if !c.cert.BasicConstraintsValid || !c.cert.IsCA || (c.cert.KeyUsage != 0 && c.cert.KeyUsage&x509.KeyUsageCertSign == 0) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.PrivatecaErrorNotCa, Params: map[string]any{"subject": c.cert.Subject.String()}})
	}
	c.chain = append(c.chain, c.cert)
	for _, cert := range certs {
		if cert != c.cert {
			c.chain = append(c.chain, cert)
		}
	}
	// Clients have the root already; it goes into no chain.
	c.chain = withoutRoots(c.chain)
	return c, nil
}

// withoutRoots drops the self-signed certificates from chain.
func withoutRoots(chain []*x509.Certificate) []*x509.Certificate {
	var out []*x509.Certificate
	for _, c := range chain {
		if !bytes.Equal(c.RawSubject, c.RawIssuer) {
			out = append(out, c)
		}
	}
	return out
}

// Close has nothing to release.
func (*ca) Close() error { return nil }

// Test makes sure the CA can sign today, changing nothing.
func (c *ca) Test(context.Context) (contracts.Message, error) {
	if err := c.valid(); err != nil {
		return contracts.Message{}, err
	}
	return contracts.Message{Key: i18n.PrivatecaTestOk, Params: map[string]any{
		"subject": c.cert.Subject.String(), "end": c.cert.NotAfter.UTC().Format(time.DateOnly),
	}}, nil
}

func (c *ca) valid() error {
	if now := c.now(); now.After(c.cert.NotAfter) || now.Before(c.cert.NotBefore) {
		return retry.Permanent(&contracts.Problem{Key: i18n.PrivatecaErrorExpired, Params: map[string]any{
			"start": c.cert.NotBefore.UTC().Format(time.DateOnly), "end": c.cert.NotAfter.UTC().Format(time.DateOnly),
		}})
	}
	return nil
}

// Issue signs a certificate for the domains and req.Key: a name that is an
// IP address goes in as one. It is valid for the days set, from an hour
// ago, for clocks that run behind, and never past the CA's own end.
func (c *ca) Issue(_ context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Domains) == 0 || req.Key == nil {
		return nil, retry.Permanent(errors.New("privateca: nothing to sign"))
	}
	if err := c.valid(); err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := c.now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    later(now.Add(-time.Hour), c.cert.NotBefore),
		NotAfter:     earlier(now.AddDate(0, 0, c.days), c.cert.NotAfter),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},

		BasicConstraintsValid: true,
	}
	if len(req.Domains[0]) <= 64 {
		tmpl.Subject = pkix.Name{CommonName: req.Domains[0]}
	}
	if _, ok := req.Key.Public().(*rsa.PublicKey); ok {
		tmpl.KeyUsage |= x509.KeyUsageKeyEncipherment
	}
	for _, d := range req.Domains {
		if ip := net.ParseIP(d); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, d)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, req.Key.Public(), c.key)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	log(contracts.LogLine{Level: contracts.OK, Step: contracts.StepOrder, Key: i18n.PrivatecaLogSigned, Params: map[string]any{
		"subject": c.cert.Subject.CommonName, "end": leaf.NotAfter.UTC().Format(time.DateOnly),
	}})
	return append([]*x509.Certificate{leaf}, c.chain...), nil
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

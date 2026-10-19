package privateca

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// newCA makes a CA certificate for key, signed by parent's key, or by its
// own when parent is nil, valid until end.
func newCA(t *testing.T, name string, key crypto.Signer, parent *x509.Certificate, parentKey crypto.Signer, end time.Time) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: end,
	}
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, key.Public(), parentKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func open(t *testing.T, certPEM, keyPEM, days string) (*ca, error) {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"days": days, "certificate": certPEM},
		Secrets: map[string]string{"key": keyPEM}})
	if err != nil {
		return nil, err
	}
	return p.(*ca), nil
}

func TestCA(t *testing.T) {
	rootKey := cryptoxtest.NewKey(t)
	root := newCA(t, "Home Root", rootKey, nil, nil, time.Now().AddDate(10, 0, 0))
	roots := x509.NewCertPool()
	roots.AddCert(root)

	c, err := open(t, cryptoxtest.CertPEM(root), cryptoxtest.KeyPEM(t, rootKey), "30")
	if err != nil {
		t.Fatal(err)
	}
	if msg, err := c.Test(t.Context()); err != nil || msg.Params["subject"] != "CN=Home Root" {
		t.Fatalf("test %+v: %v", msg, err)
	}

	// A root signs the leaf alone: clients have the root.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var lines []contracts.LogLine
	chain, err := c.Issue(t.Context(), contracts.IssueRequest{Domains: []string{"nas.home.arpa", "*.home.arpa", "192.168.1.10"}, Key: key,
		Log: func(l contracts.LogLine) { lines = append(lines, l) }})
	if err != nil || len(chain) != 1 || len(lines) != 1 {
		t.Fatalf("issued %d, logged %v: %v", len(chain), lines, err)
	}
	leaf := chain[0]
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "photos.home.arpa"}); err != nil {
		t.Error(err)
	}
	if !slices.Equal(leaf.DNSNames, []string{"nas.home.arpa", "*.home.arpa"}) || len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("192.168.1.10")) ||
		leaf.Subject.CommonName != "nas.home.arpa" || leaf.KeyUsage&x509.KeyUsageKeyEncipherment == 0 || leaf.IsCA {
		t.Errorf("leaf %v %v %q %v", leaf.DNSNames, leaf.IPAddresses, leaf.Subject.CommonName, leaf.KeyUsage)
	}
	if d := time.Until(leaf.NotAfter); d < 29*24*time.Hour || d > 30*24*time.Hour {
		t.Errorf("valid for %v", d)
	}

	// An intermediate goes into the chain, which ends before the root, and
	// no certificate outlives it.
	interKey := cryptoxtest.NewKey(t)
	inter := newCA(t, "Home Intermediate", interKey, root, rootKey, time.Now().AddDate(0, 0, 10))
	c, err = open(t, cryptoxtest.CertPEM(inter, root), cryptoxtest.KeyPEM(t, interKey), "")
	if err != nil {
		t.Fatal(err)
	}
	ecKey := cryptoxtest.NewKey(t)
	chain, err = c.Issue(t.Context(), contracts.IssueRequest{Domains: []string{"nas.home.arpa"}, Key: ecKey})
	if err != nil || len(chain) != 2 || !chain[1].Equal(inter) {
		t.Fatalf("issued %d: %v", len(chain), err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(inter)
	if _, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: pool, DNSName: "nas.home.arpa"}); err != nil {
		t.Error(err)
	}
	if !chain[0].NotAfter.Equal(inter.NotAfter) || chain[0].KeyUsage&x509.KeyUsageKeyEncipherment != 0 {
		t.Errorf("ends %v, the CA %v", chain[0].NotAfter, inter.NotAfter)
	}

	// An expired CA signs nothing.
	c.now = func() time.Time { return time.Now().AddDate(0, 0, 11) }
	if _, err := c.Issue(t.Context(), contracts.IssueRequest{Domains: []string{"nas.home.arpa"}, Key: ecKey}); err == nil {
		t.Error("an expired CA signed")
	}

	// Settings that cannot work are refused at once.
	leafPEM := cryptoxtest.CertPEM(chain[0])
	for name, s := range map[string][3]string{
		"key of another":  {cryptoxtest.CertPEM(root), cryptoxtest.KeyPEM(t, interKey), ""},
		"not a CA":        {leafPEM, cryptoxtest.KeyPEM(t, ecKey), ""},
		"no certificate":  {"nothing", cryptoxtest.KeyPEM(t, rootKey), ""},
		"no key":          {cryptoxtest.CertPEM(root), "nothing", ""},
		"too long":        {cryptoxtest.CertPEM(root), cryptoxtest.KeyPEM(t, rootKey), "900"},
		"no days":         {cryptoxtest.CertPEM(root), cryptoxtest.KeyPEM(t, rootKey), "0"},
		"days not number": {cryptoxtest.CertPEM(root), cryptoxtest.KeyPEM(t, rootKey), "a year"},
	} {
		if _, err := open(t, s[0], s[1], s[2]); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
}

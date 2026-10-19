// Package cryptoxtest issues certificates for tests: a root and an
// intermediate, and leaves with whatever names and validity a test needs.
package cryptoxtest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// CA is a root and an intermediate that signs leaves.
type CA struct {
	Root, Intermediate *x509.Certificate
	interKey           crypto.Signer
}

// NewKey returns a new ECDSA P-256 key.
func NewKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// NewCA returns a root and an intermediate valid for ten years.
func NewCA(t testing.TB) *CA {
	t.Helper()
	rootKey, interKey := NewKey(t), NewKey(t)
	now := time.Now()
	ca := func(cn string) *x509.Certificate {
		return &x509.Certificate{
			Subject: pkix.Name{CommonName: cn}, IsCA: true, BasicConstraintsValid: true,
			KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0),
		}
	}
	root := sign(t, ca("Test Root"), rootKey, nil, nil)
	inter := sign(t, ca("Test Intermediate"), interKey, root, rootKey)
	return &CA{Root: root, Intermediate: inter, interKey: interKey}
}

// Leaf issues a certificate for key, valid from notBefore to notAfter, for
// names, the first of which is also its common name.
func (ca *CA) Leaf(t testing.TB, key crypto.Signer, notBefore, notAfter time.Time, names ...string) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{
		DNSNames: names, NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if len(names) > 0 {
		tmpl.Subject.CommonName = names[0]
	}
	return sign(t, tmpl, key, ca.Intermediate, ca.interKey)
}

// Sign issues a certificate from tmpl for key with the intermediate, as a
// leaf with fields Leaf does not set, such as where to learn whether it was
// revoked.
func (ca *CA) Sign(t testing.TB, key crypto.Signer, tmpl *x509.Certificate) *x509.Certificate {
	t.Helper()
	return sign(t, tmpl, key, ca.Intermediate, ca.interKey)
}

// Signer is the key of the intermediate, to sign what it answers for its
// leaves, as OCSP responses and CRLs.
func (ca *CA) Signer() crypto.Signer { return ca.interKey }

func sign(t testing.TB, tmpl *x509.Certificate, key crypto.Signer, parent *x509.Certificate, parentKey crypto.Signer) *x509.Certificate {
	t.Helper()
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	tmpl.SerialNumber = serial
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

// CertPEM returns certs as PEM.
func CertPEM(certs ...*x509.Certificate) string {
	var out []byte
	for _, c := range certs {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return string(out)
}

// KeyPEM returns key as PKCS #8 PEM.
func KeyPEM(t testing.TB, key crypto.Signer) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

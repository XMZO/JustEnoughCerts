package cryptox

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	rsaOnce sync.Once
	rsaKey  *rsa.PrivateKey
)

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	rsaOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		rsaKey = k
	})
	return rsaKey
}

func newECKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// issue signs a certificate for key: by parent and parentKey, or by
// itself when parent is nil.
func issue(t *testing.T, tmpl *x509.Certificate, key crypto.Signer, parent *x509.Certificate, parentKey crypto.Signer) *x509.Certificate {
	t.Helper()
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl.SerialNumber = serial
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	if tmpl.NotAfter.IsZero() {
		tmpl.NotAfter = time.Now().Add(90 * 24 * time.Hour)
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

// pki is a root, an intermediate and a leaf for leafKey.
type pki struct {
	root, inter, leaf *x509.Certificate
}

func newPKI(t *testing.T, leafKey crypto.Signer) pki {
	t.Helper()
	rootKey, interKey := newECKey(t), newECKey(t)
	ca := func(cn string) *x509.Certificate {
		return &x509.Certificate{Subject: pkix.Name{CommonName: cn}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	}
	root := issue(t, ca("Test Root"), rootKey, nil, nil)
	inter := issue(t, ca("Test Intermediate"), interKey, root, rootKey)
	leaf := issue(t, &x509.Certificate{
		Subject:     pkix.Name{CommonName: "example.com"},
		DNSNames:    []string{"example.com", "www.example.com"},
		IPAddresses: []net.IP{net.ParseIP("192.0.2.7")},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, leafKey, inter, interKey)
	return pki{root, inter, leaf}
}

func pemKey(t *testing.T, key crypto.Signer) []byte {
	t.Helper()
	data, err := EncodeKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseBundle(t *testing.T) {
	key := newECKey(t)
	p := newPKI(t, key)
	unrelated := newPKI(t, newECKey(t)).root

	for name, in := range map[string]struct{ certs, key []byte }{
		"leaf first":            {EncodeCertificates(p.leaf, p.inter), pemKey(t, key)},
		"root first":            {EncodeCertificates(p.root, p.inter, p.leaf), pemKey(t, key)},
		"key in the same file":  {append(pemKey(t, key), EncodeCertificates(p.inter, p.leaf)...), nil},
		"unrelated certificate": {EncodeCertificates(unrelated, p.leaf, p.inter), pemKey(t, key)},
	} {
		b, err := ParseBundle(in.certs, in.key)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !b.Leaf.Equal(p.leaf) || len(b.Chain) != 1 || !b.Chain[0].Equal(p.inter) {
			t.Errorf("%s: leaf %s, chain %d certificates", name, b.Leaf.Subject.CommonName, len(b.Chain))
		}
	}
}

func TestParseBundleDER(t *testing.T) {
	key := newECKey(t)
	p := newPKI(t, key)
	b, err := ParseBundle(p.leaf.Raw, pemKey(t, key))
	if err != nil || !b.Leaf.Equal(p.leaf) || len(b.Chain) != 0 {
		t.Errorf("DER certificate: %v", err)
	}
}

func TestKeyEncodings(t *testing.T) {
	rsaKey := newRSAKey(t)
	ecKey := newECKey(t)
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, keyType string
		key           crypto.Signer
		pem           []byte
	}{
		{"PKCS #1", "rsa-2048", rsaKey, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})},
		{"SEC 1", "ecdsa-p256", ecKey, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER})},
		{"PKCS #8", "ed25519", edKey, pemKey(t, edKey)},
	} {
		p := newPKI(t, tt.key)
		b, err := ParseBundle(EncodeCertificates(p.leaf, p.inter), tt.pem)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got := KeyType(b.Leaf.PublicKey); got != tt.keyType {
			t.Errorf("%s: KeyType = %s, want %s", tt.name, got, tt.keyType)
		}
	}
}

func TestParseBundleErrors(t *testing.T) {
	key := newECKey(t)
	p := newPKI(t, key)
	chain := EncodeCertificates(p.leaf, p.inter)
	legacy := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-256-CBC,00"}, Bytes: []byte{1}})
	for _, tt := range []struct {
		name        string
		certs, key  []byte
		want        error
		wantMessage string
	}{
		{"no certificate", nil, pemKey(t, key), ErrNoCertificate, ""},
		{"not a certificate", []byte("hello"), pemKey(t, key), ErrNoCertificate, ""},
		{"no key", chain, nil, ErrNoKey, ""},
		{"two keys", chain, append(pemKey(t, key), pemKey(t, newECKey(t))...), ErrSeveralKeys, ""},
		{"wrong key", chain, pemKey(t, newECKey(t)), ErrKeyMismatch, ""},
		{"encrypted PKCS #8", chain, pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte{1}}), ErrEncryptedKey, ""},
		{"encrypted legacy PEM", chain, legacy, ErrEncryptedKey, ""},
		{"broken certificate", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2}}), pemKey(t, key), ErrBadCertificate, "a certificate cannot be read: x509:"},
		{"broken key", chain, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2}}), ErrBadKey, "the private key cannot be read: "},
	} {
		_, err := ParseBundle(tt.certs, tt.key)
		switch {
		case err == nil:
			t.Errorf("%s: no error", tt.name)
		case tt.want != nil && !errors.Is(err, tt.want):
			t.Errorf("%s: %v, want %v", tt.name, err, tt.want)
		case tt.wantMessage != "" && !strings.HasPrefix(err.Error(), tt.wantMessage):
			t.Errorf("%s: %v, want %q...", tt.name, err, tt.wantMessage)
		}
	}
}

func TestRoundTrips(t *testing.T) {
	key := newECKey(t)
	p := newPKI(t, key)
	certs, err := ParseCertificates(EncodeCertificates(p.leaf, p.inter))
	if err != nil || len(certs) != 2 || !certs[0].Equal(p.leaf) || !certs[1].Equal(p.inter) {
		t.Errorf("certificates: %d, %v", len(certs), err)
	}
	if _, err := ParseCertificates(nil); !errors.Is(err, ErrNoCertificate) {
		t.Errorf("no certificates: %v", err)
	}
	got, err := ParseKey(pemKey(t, key))
	if err != nil || !key.Equal(got) {
		t.Errorf("key: %v", err)
	}
	if _, err := ParseKey(EncodeCertificates(p.leaf)); !errors.Is(err, ErrNoKey) {
		t.Errorf("no key: %v", err)
	}
}

func TestNamesAndFingerprint(t *testing.T) {
	p := newPKI(t, newECKey(t))
	if got := strings.Join(Names(p.leaf), ","); got != "example.com,www.example.com,192.0.2.7" {
		t.Errorf("Names = %s", got)
	}
	if got := strings.Join(Names(p.root), ","); got != "Test Root" {
		t.Errorf("Names without SANs = %s", got)
	}
	sum := sha256.Sum256(p.leaf.Raw)
	if got := Fingerprint(p.leaf); got != hex.EncodeToString(sum[:]) || len(got) != 64 {
		t.Errorf("Fingerprint = %s", got)
	}
	if !bytes.Contains(EncodeCertificates(p.leaf), []byte("BEGIN CERTIFICATE")) {
		t.Error("EncodeCertificates does not write PEM")
	}
}

func TestGenerateKey(t *testing.T) {
	for _, kt := range KeyTypes {
		if testing.Short() && strings.HasPrefix(kt, "rsa-") && kt != "rsa-2048" {
			continue // slow
		}
		key, err := GenerateKey(kt)
		if err != nil {
			t.Fatalf("%s: %v", kt, err)
		}
		if got := KeyType(key.Public()); got != kt {
			t.Errorf("GenerateKey(%s) made a %s key", kt, got)
		}
	}
	if _, err := GenerateKey("dsa-1024"); !errors.Is(err, ErrUnsupportedKey) {
		t.Errorf("an unknown type: %v", err)
	}
}

func TestNewBundle(t *testing.T) {
	key := newECKey(t)
	p := newPKI(t, key)
	b, err := NewBundle([]*x509.Certificate{p.root, p.inter, p.leaf}, key)
	if err != nil {
		t.Fatal(err)
	}
	if b.Leaf != p.leaf || len(b.Chain) != 1 || b.Chain[0] != p.inter {
		t.Errorf("leaf %v, chain of %d", b.Leaf.Subject, len(b.Chain))
	}
	if _, err := NewBundle([]*x509.Certificate{p.leaf}, newECKey(t)); !errors.Is(err, ErrKeyMismatch) {
		t.Errorf("another key: %v", err)
	}
	if _, err := NewBundle(nil, key); !errors.Is(err, ErrNoCertificate) {
		t.Errorf("no certificates: %v", err)
	}
}

func TestCSR(t *testing.T) {
	key := newECKey(t)
	data, err := CSR(key, []string{"example.com", "*.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(rest) != 0 {
		t.Fatalf("PEM %q", data)
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Error(err)
	}
	if csr.Subject.CommonName != "example.com" || strings.Join(csr.DNSNames, " ") != "example.com *.example.com" || !key.PublicKey.Equal(csr.PublicKey) {
		t.Errorf("CSR of %s for %v", csr.Subject, csr.DNSNames)
	}
}

func TestEncodeKeyTraditional(t *testing.T) {
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		key   crypto.Signer
		block string
	}{
		{newRSAKey(t), "RSA PRIVATE KEY"},
		{newECKey(t), "EC PRIVATE KEY"},
		{edKey, "PRIVATE KEY"},
	} {
		data, err := EncodeKeyTraditional(tt.key)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := pem.Decode(data); b == nil || b.Type != tt.block {
			t.Errorf("%T: block %v", tt.key, b)
		}
		back, err := ParseKey(data)
		if err != nil || !back.Public().(interface{ Equal(crypto.PublicKey) bool }).Equal(tt.key.Public()) {
			t.Errorf("%T: read back %v", tt.key, err)
		}
	}
}

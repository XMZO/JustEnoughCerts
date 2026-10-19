package cryptox

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestCompleteChain(t *testing.T) {
	served := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if data, ok := served[r.URL.Path]; ok {
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	now := time.Now()
	issue := func(cn string, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, ca bool, aia string) (*x509.Certificate, *ecdsa.PrivateKey) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: cn}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
			IsCA: ca, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		}
		if aia != "" {
			tmpl.IssuingCertificateURL = []string{srv.URL + aia}
		}
		if parent == nil {
			parent, parentKey = tmpl, key
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, key.Public(), parentKey)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := x509.ParseCertificate(der)
		return c, key
	}
	root, rootKey := issue("Root", nil, nil, true, "")
	upper, upperKey := issue("Upper", root, rootKey, true, "/root.der")
	lower, lowerKey := issue("Lower", upper, upperKey, true, "/upper.pem")
	leaf, _ := issue("leaf.example.com", lower, lowerKey, false, "/lower.der")
	served["/root.der"] = root.Raw
	served["/upper.pem"] = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upper.Raw})
	served["/lower.der"] = lower.Raw

	b := &Bundle{Leaf: leaf}
	if err := CompleteChain(t.Context(), srv.Client(), b); err != nil || len(b.Chain) != 2 ||
		b.Chain[0].Subject.CommonName != "Lower" || b.Chain[1].Subject.CommonName != "Upper" {
		t.Fatalf("chain %v, %v", b.Chain, err)
	}
	// A wrong certificate is refused, and the chain left as it was.
	served["/lower.der"] = upper.Raw
	b = &Bundle{Leaf: leaf}
	if err := CompleteChain(t.Context(), srv.Client(), b); err == nil || len(b.Chain) != 0 {
		t.Errorf("a wrong issuer: %v %v", b.Chain, err)
	}
}

func TestPKCS7Certificates(t *testing.T) {
	// A .p7c as openssl crl2pkcs7 makes it, of one certificate.
	p7c, err := os.ReadFile("testdata/one.p7c")
	if err != nil {
		t.Fatal(err)
	}
	if certs := pkcs7Certificates(p7c); len(certs) != 1 || certs[0].Subject.CommonName != "In a p7c" {
		t.Errorf("certificates %v", certs)
	}
	if certs := pkcs7Certificates([]byte("nothing")); certs != nil {
		t.Errorf("from nothing: %v", certs)
	}
}

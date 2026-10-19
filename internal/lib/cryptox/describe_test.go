package cryptox

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"slices"
	"testing"
	"time"
)

func TestDescribe(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dv, err := x509.ParseOID("2.23.140.1.2.1")
	if err != nil {
		t.Fatal(err)
	}
	// Two timestamps of three bytes and one, each after its length, the
	// list after its own.
	scts, _ := asn1.Marshal([]byte{0, 8, 0, 3, 'a', 'b', 'c', 0, 1, 'x'})
	staple, _ := asn1.Marshal([]int{5})
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "example.com", Organization: []string{"Example"}},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"example.com"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		OCSPServer: []string{"http://ocsp.example"}, CRLDistributionPoints: []string{"http://crl.example/ca.crl"},
		IssuingCertificateURL: []string{"http://ca.example/ca.crt"}, Policies: []x509.OID{dv},
		ExtraExtensions: []pkix.Extension{{Id: oidSCTs, Value: scts}, {Id: oidTLSFeature, Value: staple}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	d := Describe(c)
	switch {
	case d.Subject != "CN=example.com,O=Example" || d.Issuer != d.Subject || d.SignatureAlgorithm != "ECDSA-SHA256":
		t.Errorf("names %+v", d)
	case len(d.SHA1) != 40 || len(d.SPKISHA256) != 44:
		t.Errorf("fingerprints %q %q", d.SHA1, d.SPKISHA256)
	case !slices.Equal(d.KeyUsage, []string{"digital_signature"}) || !slices.Equal(d.ExtKeyUsage, []string{"server_auth", "client_auth"}):
		t.Errorf("uses %v %v", d.KeyUsage, d.ExtKeyUsage)
	case d.OCSP[0] != "http://ocsp.example" || d.CRL[0] != "http://crl.example/ca.crl" || d.CAIssuers[0] != "http://ca.example/ca.crt":
		t.Errorf("locations %+v", d)
	case d.Validation != "dv" || d.SCTs != 2 || !d.MustStaple:
		t.Errorf("validation %q, %d timestamps, must staple %v", d.Validation, d.SCTs, d.MustStaple)
	}

	// A certificate that says little: lists, not nils, to send.
	bare := Describe(&x509.Certificate{})
	if bare.OCSP == nil || bare.KeyUsage == nil || bare.Validation != "" || bare.SCTs != 0 {
		t.Errorf("bare %+v", bare)
	}
	if countSCTs([]byte{0x04, 0x02, 0, 9}) != 0 {
		t.Error("a list longer than its data")
	}

	csr, err := CSR(key, []string{"example.com", "www.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := DescribeCSR(csr)
	if err != nil {
		t.Fatal(err)
	}
	if r.Subject != "CN=example.com" || !slices.Equal(r.Names, []string{"example.com", "www.example.com"}) || r.KeyType != "ecdsa-p256" || r.SignatureAlgorithm != "ECDSA-SHA256" {
		t.Errorf("request %+v", r)
	}
	if _, err := DescribeCSR([]byte("not a request")); err == nil {
		t.Error("read garbage as a request")
	}
}

package cryptox

import (
	"crypto/sha1" //nolint:gosec // G505: SHA-1 thumbprints name certificates, as Windows does; they protect nothing
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"slices"
)

// Details are what a certificate says beyond its names and dates, as a page
// of it shows them.
type Details struct {
	// Subject and Issuer are distinguished names, as RFC 2253 writes them.
	Subject, Issuer string
	// SignatureAlgorithm is as Go names it: SHA256-RSA, ECDSA-SHA384.
	SignatureAlgorithm string
	// SHA1 is the certificate's thumbprint as Windows shows it, in hex;
	// SPKISHA256 the SHA-256 of its public key, in base64, as pins are.
	SHA1, SPKISHA256 string
	// KeyUsage and ExtKeyUsage name the uses the certificate allows, as
	// KeyUsages and ExtKeyUsages do.
	KeyUsage, ExtKeyUsage []string
	// OCSP, CRL and CAIssuers are where its revocation is looked up and its
	// issuer's certificate fetched.
	OCSP, CRL, CAIssuers []string
	// Validation is what the CA checked by the policy of the CA/Browser
	// Forum: dv, ov, iv or ev; empty when the certificate does not say.
	Validation string
	// SCTs counts the timestamps of Certificate Transparency logs embedded.
	SCTs int
	// MustStaple tells that clients must get its OCSP status from the server.
	MustStaple bool
}

// KeyUsages are the names of the key uses Describe tells, by bit.
var KeyUsages = []struct {
	Bit  x509.KeyUsage
	Name string
}{
	{x509.KeyUsageDigitalSignature, "digital_signature"},
	{x509.KeyUsageContentCommitment, "content_commitment"},
	{x509.KeyUsageKeyEncipherment, "key_encipherment"},
	{x509.KeyUsageDataEncipherment, "data_encipherment"},
	{x509.KeyUsageKeyAgreement, "key_agreement"},
	{x509.KeyUsageCertSign, "cert_sign"},
	{x509.KeyUsageCRLSign, "crl_sign"},
	{x509.KeyUsageEncipherOnly, "encipher_only"},
	{x509.KeyUsageDecipherOnly, "decipher_only"},
}

// ExtKeyUsages are the names of the extended key uses Describe tells.
var ExtKeyUsages = map[x509.ExtKeyUsage]string{
	x509.ExtKeyUsageAny:             "any",
	x509.ExtKeyUsageServerAuth:      "server_auth",
	x509.ExtKeyUsageClientAuth:      "client_auth",
	x509.ExtKeyUsageCodeSigning:     "code_signing",
	x509.ExtKeyUsageEmailProtection: "email_protection",
	x509.ExtKeyUsageTimeStamping:    "time_stamping",
	x509.ExtKeyUsageOCSPSigning:     "ocsp_signing",
}

// The object identifiers Describe reads: the policies of the CA/Browser
// Forum, the timestamps of Certificate Transparency (RFC 6962) and the TLS
// feature of OCSP Must-Staple (RFC 7633).
var (
	policies = map[string]string{
		"2.23.140.1.1":   "ev",
		"2.23.140.1.2.1": "dv",
		"2.23.140.1.2.2": "ov",
		"2.23.140.1.2.3": "iv",
	}
	oidSCTs       = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}
	oidTLSFeature = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 24}
)

// Describe tells what c says beyond its names and dates.
func Describe(c *x509.Certificate) Details {
	sha := sha1.Sum(c.Raw) //nolint:gosec // G401: a thumbprint, not a protection
	spki := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	d := Details{
		Subject: c.Subject.String(), Issuer: c.Issuer.String(), SignatureAlgorithm: c.SignatureAlgorithm.String(),
		SHA1: hex.EncodeToString(sha[:]), SPKISHA256: base64.StdEncoding.EncodeToString(spki[:]),
		KeyUsage: []string{}, ExtKeyUsage: []string{},
		OCSP: slices.Clone(c.OCSPServer), CRL: slices.Clone(c.CRLDistributionPoints), CAIssuers: slices.Clone(c.IssuingCertificateURL),
	}
	for _, u := range KeyUsages {
		if c.KeyUsage&u.Bit != 0 {
			d.KeyUsage = append(d.KeyUsage, u.Name)
		}
	}
	for _, u := range c.ExtKeyUsage {
		if name, ok := ExtKeyUsages[u]; ok {
			d.ExtKeyUsage = append(d.ExtKeyUsage, name)
		}
	}
	for _, p := range c.Policies {
		if v, ok := policies[p.String()]; ok {
			d.Validation = v
			break
		}
	}
	for _, e := range c.Extensions {
		switch {
		case e.Id.Equal(oidSCTs):
			d.SCTs = countSCTs(e.Value)
		case e.Id.Equal(oidTLSFeature):
			var features []int
			if _, err := asn1.Unmarshal(e.Value, &features); err == nil {
				d.MustStaple = slices.Contains(features, 5) // status_request
			}
		}
	}
	for _, s := range []*[]string{&d.OCSP, &d.CRL, &d.CAIssuers} {
		if *s == nil {
			*s = []string{}
		}
	}
	return d
}

// countSCTs counts the timestamps in the value of the extension that
// embeds them: an OCTET STRING holding a list, each item after its length
// in two bytes, as the whole list is.
func countSCTs(value []byte) int {
	var list []byte
	if _, err := asn1.Unmarshal(value, &list); err != nil || len(list) < 2 {
		return 0
	}
	n := int(binary.BigEndian.Uint16(list))
	list = list[2:]
	if n > len(list) {
		return 0
	}
	count := 0
	for rest := list[:n]; len(rest) >= 2; count++ {
		size := int(binary.BigEndian.Uint16(rest))
		if 2+size > len(rest) {
			return count
		}
		rest = rest[2+size:]
	}
	return count
}

// Request is what a certificate request says, as a page shows it.
type Request struct {
	Subject            string
	Names              []string
	KeyType            string
	SignatureAlgorithm string
}

// DescribeCSR reads a certificate request in PEM form.
func DescribeCSR(data []byte) (Request, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return Request{}, ErrBadCertificate
	}
	r, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return Request{}, err
	}
	return Request{
		Subject: r.Subject.String(), Names: slices.Clone(r.DNSNames), KeyType: KeyType(r.PublicKey), SignatureAlgorithm: r.SignatureAlgorithm.String(),
	}, nil
}

package cryptox

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"io"
	"net/http"
)

// maxIssuers bounds the certificates CompleteChain fetches, and maxIssuer
// the bytes of each.
const (
	maxIssuers = 4
	maxIssuer  = 64 << 10
)

// CompleteChain fetches the issuers of b that its chain lacks, from the
// "CA Issuers" address each certificate names (AIA), up to a self-signed
// root, which it leaves out, as clients have their own. It stops quietly
// where a certificate names no address; it fails when one cannot be
// fetched or does not issue the certificate, keeping those it had.
func CompleteChain(ctx context.Context, client *http.Client, b *Bundle) error {
	cur := b.Leaf
	if len(b.Chain) > 0 {
		cur = b.Chain[len(b.Chain)-1]
	}
	for range maxIssuers {
		if isSelfSigned(cur) || len(cur.IssuingCertificateURL) == 0 {
			return nil
		}
		issuer, err := fetchIssuer(ctx, client, cur)
		if err != nil {
			return err
		}
		if isSelfSigned(issuer) {
			return nil
		}
		b.Chain = append(b.Chain, issuer)
		cur = issuer
	}
	return nil
}

// fetchIssuer fetches the issuer of c from the address c names, in DER,
// PEM or PKCS #7, as Sectigo serves it: of those, the one that signed c.
func fetchIssuer(ctx context.Context, client *http.Client, c *x509.Certificate) (*x509.Certificate, error) {
	url := c.IssuingCertificateURL[0]
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cryptox: %s answered HTTP %d", url, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxIssuer))
	if err != nil {
		return nil, err
	}
	var found []*x509.Certificate
	if one, err := x509.ParseCertificate(data); err == nil {
		found = []*x509.Certificate{one}
	} else if certs, _, err := parsePEM(data); err == nil && len(certs) > 0 {
		found = certs
	} else {
		found = pkcs7Certificates(data)
	}
	for _, issuer := range found {
		if bytes.Equal(c.RawIssuer, issuer.RawSubject) && c.CheckSignatureFrom(issuer) == nil {
			return issuer, nil
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("cryptox: %s served no certificate", url)
	}
	return nil, fmt.Errorf("cryptox: %s served no certificate that issued %s", url, c.Subject)
}

// pkcs7Certificates reads the certificates of a PKCS #7 SignedData
// without signers (RFC 2315), as a .p7c file holds; none when it is not
// one.
func pkcs7Certificates(data []byte) []*x509.Certificate {
	var info struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue `asn1:"explicit,tag:0"`
	}
	if _, err := asn1.Unmarshal(data, &info); err != nil {
		return nil
	}
	var signed struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		ContentInfo      asn1.RawValue
		Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	}
	if _, err := asn1.Unmarshal(info.Content.Bytes, &signed); err != nil {
		return nil
	}
	certs, err := x509.ParseCertificates(signed.Certificates.Bytes)
	if err != nil {
		return nil
	}
	return certs
}

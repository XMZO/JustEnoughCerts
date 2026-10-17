// Package cryptox reads and writes certificates and private keys: the PEM
// files people upload, the chains stored with each certificate, and the
// check that a key belongs to its certificate. It holds no business logic.
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
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
)

// Errors of ParseBundle, for callers to tell the user what is wrong.
var (
	ErrNoCertificate  = errors.New("no certificate found")
	ErrBadCertificate = errors.New("a certificate cannot be read")
	ErrNoKey          = errors.New("no private key found")
	ErrBadKey         = errors.New("the private key cannot be read")
	ErrEncryptedKey   = errors.New("the private key is encrypted")
	ErrSeveralKeys    = errors.New("more than one private key")
	ErrKeyMismatch    = errors.New("the private key belongs to none of the certificates")
	ErrUnsupportedKey = errors.New("unsupported private key type")
)

// Bundle is a certificate, the intermediates that lead to its root, and
// its private key.
type Bundle struct {
	Leaf *x509.Certificate
	// Chain holds the intermediates, the leaf's issuer first. A self-signed
	// root is left out: clients have their own.
	Chain []*x509.Certificate
	Key   crypto.Signer
}

// ParseBundle reads certificates in PEM or, for a single certificate, DER,
// with the private key in the same data or in key. The certificates may
// come in any order; the leaf is the one the key belongs to, and the chain
// is built from the others by issuer, ignoring any that are not part of
// it.
func ParseBundle(certs, key []byte) (*Bundle, error) {
	var found []*x509.Certificate
	var keys []crypto.Signer
	for _, data := range [][]byte{certs, key} {
		c, k, err := parsePEM(data)
		if err != nil {
			return nil, err
		}
		found = append(found, c...)
		keys = append(keys, k...)
	}
	if len(found) == 0 && len(bytes.TrimSpace(certs)) > 0 {
		if c, err := x509.ParseCertificate(certs); err == nil {
			found = append(found, c)
		}
	}
	switch {
	case len(found) == 0:
		return nil, ErrNoCertificate
	case len(keys) == 0:
		return nil, ErrNoKey
	case len(keys) > 1:
		return nil, ErrSeveralKeys
	}
	return NewBundle(found, keys[0])
}

// NewBundle makes the bundle of key out of certs, in any order: the leaf is
// the certificate key belongs to, and the chain is built from the others
// as ParseBundle builds it.
func NewBundle(certs []*x509.Certificate, key crypto.Signer) (*Bundle, error) {
	if len(certs) == 0 {
		return nil, ErrNoCertificate
	}
	b := &Bundle{Key: key}
	type equaler interface{ Equal(crypto.PublicKey) bool }
	pub, ok := key.Public().(equaler)
	if !ok {
		return nil, ErrUnsupportedKey
	}
	for _, c := range certs {
		if pub.Equal(c.PublicKey) {
			b.Leaf = c
			break
		}
	}
	if b.Leaf == nil {
		return nil, ErrKeyMismatch
	}
	b.Chain = buildChain(b.Leaf, certs)
	return b, nil
}

// parsePEM returns the certificates and private keys in data.
func parsePEM(data []byte) ([]*x509.Certificate, []crypto.Signer, error) {
	var certs []*x509.Certificate
	var keys []crypto.Signer
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return certs, keys, nil
		}
		switch block.Type {
		case "CERTIFICATE":
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrBadCertificate, err)
			}
			certs = append(certs, c)
		case "ENCRYPTED PRIVATE KEY":
			return nil, nil, ErrEncryptedKey
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			if _, encrypted := block.Headers["DEK-Info"]; encrypted {
				return nil, nil, ErrEncryptedKey
			}
			k, err := parseKey(block)
			if err != nil {
				return nil, nil, err
			}
			keys = append(keys, k)
		}
	}
}

func parseKey(block *pem.Block) (crypto.Signer, error) {
	var key any
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadKey, err)
	}
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return k, nil
	case *ecdsa.PrivateKey:
		return k, nil
	case ed25519.PrivateKey:
		return k, nil
	}
	return nil, ErrUnsupportedKey
}

// buildChain follows issuers from leaf through certs, up to a self-signed
// root, which it leaves out.
func buildChain(leaf *x509.Certificate, certs []*x509.Certificate) []*x509.Certificate {
	var chain []*x509.Certificate
	used := map[*x509.Certificate]bool{leaf: true}
	for cur := leaf; ; {
		var next *x509.Certificate
		for _, c := range certs {
			if !used[c] && bytes.Equal(cur.RawIssuer, c.RawSubject) && cur.CheckSignatureFrom(c) == nil {
				next = c
				break
			}
		}
		if next == nil || isSelfSigned(next) {
			return chain
		}
		used[next] = true
		chain = append(chain, next)
		cur = next
	}
}

func isSelfSigned(c *x509.Certificate) bool {
	return bytes.Equal(c.RawIssuer, c.RawSubject) && c.CheckSignatureFrom(c) == nil
}

// ParseCertificates reads every certificate in PEM data, in order.
func ParseCertificates(data []byte) ([]*x509.Certificate, error) {
	certs, _, err := parsePEM(data)
	if err != nil {
		return nil, err
	}
	if len(certs) == 0 {
		return nil, ErrNoCertificate
	}
	return certs, nil
}

// ParseKey reads the one private key in PEM data.
func ParseKey(data []byte) (crypto.Signer, error) {
	_, keys, err := parsePEM(data)
	switch {
	case err != nil:
		return nil, err
	case len(keys) == 0:
		return nil, ErrNoKey
	case len(keys) > 1:
		return nil, ErrSeveralKeys
	}
	return keys[0], nil
}

// EncodeCertificates returns certs as PEM, in order.
func EncodeCertificates(certs ...*x509.Certificate) []byte {
	var b bytes.Buffer
	for _, c := range certs {
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	return b.Bytes()
}

// EncodeKey returns key as PKCS #8 PEM.
func EncodeKey(key crypto.Signer) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// KeyType names the type and size of a public key as JEC stores it:
// rsa-2048, ecdsa-p256, ed25519 and so on.
func KeyType(pub crypto.PublicKey) string {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return "rsa-" + strconv.Itoa(k.N.BitLen())
	case *ecdsa.PublicKey:
		switch k.Curve {
		case elliptic.P256():
			return "ecdsa-p256"
		case elliptic.P384():
			return "ecdsa-p384"
		case elliptic.P521():
			return "ecdsa-p521"
		}
		return "ecdsa"
	case ed25519.PublicKey:
		return "ed25519"
	}
	return "unknown"
}

// Fingerprint returns the SHA-256 of the certificate, in lowercase hex.
func Fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

// Names returns the names a certificate is valid for: its DNS names and IP
// addresses, or its common name when it has neither.
func Names(c *x509.Certificate) []string {
	names := append([]string(nil), c.DNSNames...)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	if len(names) == 0 && c.Subject.CommonName != "" {
		names = append(names, c.Subject.CommonName)
	}
	return names
}

// KeyTypes are the types of key GenerateKey makes, as KeyType names them.
var KeyTypes = []string{"ecdsa-p256", "ecdsa-p384", "rsa-2048", "rsa-3072", "rsa-4096"}

// GenerateKey makes a new private key of keyType, one of KeyTypes.
func GenerateKey(keyType string) (crypto.Signer, error) {
	switch keyType {
	case "ecdsa-p256":
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "ecdsa-p384":
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "rsa-2048":
		return rsa.GenerateKey(rand.Reader, 2048)
	case "rsa-3072":
		return rsa.GenerateKey(rand.Reader, 3072)
	case "rsa-4096":
		return rsa.GenerateKey(rand.Reader, 4096)
	}
	return nil, fmt.Errorf("%w: %q", ErrUnsupportedKey, keyType)
}

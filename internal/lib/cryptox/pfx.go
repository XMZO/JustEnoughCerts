package cryptox

import (
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"

	"software.sslmate.com/src/go-pkcs12"
)

// Errors of ParsePFX, beside those of NewBundle.
var (
	ErrPFXPassword = errors.New("the password of the PKCS #12 file is wrong")
	ErrBadPFX      = errors.New("the PKCS #12 file cannot be read")
)

// ParsePFX reads a PKCS #12 file (.pfx, .p12), as Windows, IIS and many
// panels export a certificate: the leaf, its chain and its private key,
// encrypted with password, which may be empty. Both the legacy encryption
// and the AES of OpenSSL 3 are read.
func ParsePFX(data []byte, password string) (*Bundle, error) {
	key, leaf, chain, err := pkcs12.DecodeChain(data, password)
	switch {
	case errors.Is(err, pkcs12.ErrIncorrectPassword):
		return nil, ErrPFXPassword
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrBadPFX, err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, ErrUnsupportedKey
	}
	return NewBundle(append([]*x509.Certificate{leaf}, chain...), signer)
}

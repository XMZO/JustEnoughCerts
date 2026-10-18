package cryptox_test

import (
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"

	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

func TestParsePFX(t *testing.T) {
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	now := time.Now()
	leaf := ca.Leaf(t, key, now.Add(-time.Hour), now.Add(90*24*time.Hour), "example.com")
	chain := []*x509.Certificate{ca.Intermediate}

	for name, enc := range map[string]*pkcs12.Encoder{
		"OpenSSL 3": pkcs12.Modern, "legacy": pkcs12.LegacyRC2, "legacy DES": pkcs12.LegacyDES,
	} {
		data, err := enc.Encode(key, leaf, chain, "correct horse")
		if err != nil {
			t.Fatal(err)
		}
		b, err := cryptox.ParsePFX(data, "correct horse")
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !b.Leaf.Equal(leaf) || len(b.Chain) != 1 || !b.Chain[0].Equal(ca.Intermediate) {
			t.Errorf("%s: leaf %s, chain of %d", name, b.Leaf.Subject, len(b.Chain))
		}
		if _, err := cryptox.ParsePFX(data, "wrong horse"); !errors.Is(err, cryptox.ErrPFXPassword) {
			t.Errorf("%s with a wrong password: %v", name, err)
		}
	}

	data, err := pkcs12.Passwordless.Encode(key, leaf, chain, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cryptox.ParsePFX(data, ""); err != nil {
		t.Errorf("without a password: %v", err)
	}

	// The key of another certificate.
	data, err = pkcs12.Modern.Encode(cryptoxtest.NewKey(t), leaf, chain, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cryptox.ParsePFX(data, ""); !errors.Is(err, cryptox.ErrKeyMismatch) {
		t.Errorf("a key of another certificate: %v", err)
	}
	if _, err := cryptox.ParsePFX([]byte("not a PFX file"), ""); !errors.Is(err, cryptox.ErrBadPFX) {
		t.Errorf("not a PFX file: %v", err)
	}
}

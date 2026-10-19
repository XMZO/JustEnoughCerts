package cryptox_test

import (
	"errors"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

func TestFindPairs(t *testing.T) {
	ca := cryptoxtest.NewCA(t)
	now := time.Now()
	k1, k2, k3 := cryptoxtest.NewKey(t), cryptoxtest.NewKey(t), cryptoxtest.NewKey(t)
	a := ca.Leaf(t, k1, now, now.Add(90*24*time.Hour), "a.example.com")
	b := ca.Leaf(t, k2, now, now.Add(90*24*time.Hour), "b.example.com")
	c := ca.Leaf(t, k3, now, now.Add(90*24*time.Hour), "c.example.com")
	files := [][]byte{
		[]byte(cryptoxtest.CertPEM(a, ca.Intermediate)), // a's chain
		[]byte(cryptoxtest.KeyPEM(t, k1)),               // a's key, alone
		[]byte(cryptoxtest.CertPEM(b) + cryptoxtest.KeyPEM(t, k2) + cryptoxtest.CertPEM(ca.Intermediate)),
		c.Raw, // DER, without its key
		[]byte(cryptoxtest.KeyPEM(t, cryptoxtest.NewKey(t))), // a key of nothing here
		[]byte("not a certificate"),
		[]byte(cryptoxtest.CertPEM(a)), // a copy
	}
	p, bad := cryptox.FindPairs(files)
	if len(p.Bundles) != 2 || p.Bundles[0].Leaf.Subject.CommonName != "a.example.com" || p.Bundles[1].Leaf.Subject.CommonName != "b.example.com" ||
		len(p.Bundles[0].Chain) != 1 || len(p.Bundles[1].Chain) != 1 {
		t.Fatalf("bundles %+v", p.Bundles)
	}
	if len(p.Alone) != 1 || p.Alone[0].Subject.CommonName != "c.example.com" {
		t.Errorf("alone %+v", p.Alone)
	}
	if len(bad) != 1 || !errors.Is(bad[5], cryptox.ErrNoCertificate) {
		t.Errorf("bad %v", bad)
	}
}

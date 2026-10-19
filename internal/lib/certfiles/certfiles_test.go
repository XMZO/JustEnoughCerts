package certfiles

import (
	"bytes"
	"crypto/x509"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

func TestParse(t *testing.T) {
	// Left out, the settings are certbot's.
	f, err := Parse(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Files{cert: "cert.pem", chain: "chain.pem", fullChain: "fullchain.pem", key: "privkey.pem", certMode: 0o644, keyMode: 0o600}); f != want {
		t.Errorf("defaults %+v", f)
	}

	for _, c := range []struct {
		name   string
		config map[string]string
		key    i18n.Key
	}{
		{"a path", map[string]string{keyCert: "../cert.pem"}, i18n.CertfilesErrorName},
		{"a directory", map[string]string{keyKey: ".."}, i18n.CertfilesErrorName},
		{"a backslash", map[string]string{keyPFX: `certs\a.pfx`}, i18n.CertfilesErrorName},
		{"one name twice", map[string]string{keyCombined: "fullchain.pem"}, i18n.CertfilesErrorSame},
		{"a mode that is not octal", map[string]string{keyCertMode: "rw-r--r--"}, i18n.CertfilesErrorMode},
		{"a mode too large", map[string]string{keyCertMode: "4755"}, i18n.CertfilesErrorMode},
		{"a key everyone reads", map[string]string{keyKeyMode: "644"}, i18n.CertfilesErrorKeyMode},
	} {
		_, err := Parse(c.config, nil)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestMake(t *testing.T) {
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	now := time.Now()
	leaf := ca.Leaf(t, key, now.Add(-time.Hour), now.Add(90*24*time.Hour), "*.example.com")
	chain := []*x509.Certificate{leaf, ca.Intermediate}

	f, err := Parse(map[string]string{
		keyCert: "{domain}.crt", keyKey: "{domain}.key", keyCombined: "{domain}.pem", keyPFX: "{domain}.pfx",
		keyCertMode: "640", keyKeyMode: "640", keyCipher: cipherLegacy,
	}, map[string]string{keyPassword: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	files, err := f.Make([]string{"*.example.com", "example.com"}, chain, key)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]File{}
	for _, file := range files {
		byKind[file.Kind] = file
	}
	for kind, want := range map[string]struct {
		name   string
		mode   fs.FileMode
		secret bool
	}{
		KindCert: {"star.example.com.crt", 0o640, false}, KindChain: {"chain.pem", 0o640, false}, KindFullChain: {"fullchain.pem", 0o640, false},
		KindKey: {"star.example.com.key", 0o640, true}, KindCombined: {"star.example.com.pem", 0o640, true}, KindPFX: {"star.example.com.pfx", 0o640, true},
	} {
		got, ok := byKind[kind]
		if !ok || got.Name != want.name || got.Mode != want.mode || got.Secret != want.secret {
			t.Errorf("%s: %+v", kind, got)
		}
	}
	if len(files) != 6 {
		t.Errorf("%d files", len(files))
	}

	// What each holds.
	if !bytes.Equal(byKind[KindCert].Data, cryptox.EncodeCertificates(leaf)) || !bytes.Equal(byKind[KindChain].Data, cryptox.EncodeCertificates(ca.Intermediate)) ||
		!bytes.Equal(byKind[KindFullChain].Data, cryptox.EncodeCertificates(leaf, ca.Intermediate)) {
		t.Error("the certificates are not where they belong")
	}
	pem, _ := cryptox.EncodeKey(key)
	if !bytes.Equal(byKind[KindKey].Data, pem) || !bytes.Equal(byKind[KindCombined].Data, append(cryptox.EncodeCertificates(leaf, ca.Intermediate), pem...)) {
		t.Error("the key is not where it belongs")
	}
	b, err := cryptox.ParsePFX(byKind[KindPFX].Data, "correct horse")
	if err != nil || !b.Leaf.Equal(leaf) || len(b.Chain) != 1 {
		t.Errorf("the PFX file: %v", err)
	}
	// One that holds it all reads back as the certificate and its key.
	if b, err := cryptox.ParseBundle(byKind[KindCombined].Data, nil); err != nil || !b.Leaf.Equal(leaf) {
		t.Errorf("the one file: %v", err)
	}

	names, err := f.Names([]string{"*.example.com"})
	if err != nil || names[KindCert] != "star.example.com.crt" || names[KindPFX] != "star.example.com.pfx" || len(names) != 6 {
		t.Errorf("names %v, %v", names, err)
	}

	// The defaults leave the one file and the PFX file out.
	plain, _ := Parse(nil, nil)
	if files, err := plain.Make([]string{"example.com"}, chain, key); err != nil || len(files) != 4 || files[3].Name != "privkey.pem" || files[3].Mode != 0o600 {
		t.Errorf("by default: %d files, %v", len(files), err)
	}
	if names, _ := plain.Names([]string{"example.com"}); len(names) != 4 {
		t.Errorf("names by default: %v", names)
	}
}

func TestDomainName(t *testing.T) {
	for in, want := range map[string]string{
		"example.com":          "example.com",
		"*.Example.com":        "star.example.com",
		"xn--fiqs8s.example":   "xn--fiqs8s.example",
		"under_score.example":  "under_score.example",
		"a b/c.example.com":    "a_b_c.example.com",
		"weird*.*.example.com": "weirdstar._.example.com", // only the first * is a word
	} {
		if got, err := DomainName([]string{in}); err != nil || got != want {
			t.Errorf("DomainName(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{".", ".."} {
		if _, err := DomainName([]string{bad}); err == nil {
			t.Errorf("DomainName(%q) made a name", bad)
		}
	}
	if _, err := DomainName(nil); err == nil {
		t.Error("a name without domains")
	}
}

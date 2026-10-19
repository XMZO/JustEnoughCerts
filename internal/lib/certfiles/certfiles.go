// Package certfiles makes the files a certificate is written as by the
// targets that write files, as a local directory, a server over SSH or a
// container (PLAN.md 5.3.3 and 11): the certificate, the chain, the full
// chain and the private key under names people may change, and when asked
// the private key with the full chain in one file, as HAProxy reads it,
// and a PKCS #12 file, as Windows and Java read it.
package certfiles

import (
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// The keys of the settings.
const (
	keyCert      = "cert_file"
	keyChain     = "chain_file"
	keyFullChain = "fullchain_file"
	keyKey       = "key_file"
	keyCombined  = "combined_file"
	keyPFX       = "pfx_file"
	keyPassword  = "pfx_password"
	keyCipher    = "pfx_cipher"
	keyCertMode  = "cert_mode"
	keyKeyMode   = "key_mode"
)

// Ciphers of a PKCS #12 file.
const (
	cipherModern = "modern" // AES-256, as OpenSSL 3 writes it
	cipherLegacy = "legacy" // 3DES, for Windows Server 2016 and Java 8
)

// The names and modes of the files no one changed: certbot's.
const (
	defaultCert      = "cert.pem"
	defaultChain     = "chain.pem"
	defaultFullChain = "fullchain.pem"
	defaultKey       = "privkey.pem"
	defaultCertMode  = "644"
	defaultKeyMode   = "600"
)

// Placeholder is the part of a name that the primary domain of the
// certificate takes, as DomainName writes it.
const Placeholder = "{domain}"

// Fields are the settings of the files, all advanced, for the types of the
// targets that write them to add to theirs.
func Fields() []providers.Field {
	return []providers.Field{
		{Key: keyCert, Label: i18n.CertfilesFieldCert, Help: i18n.CertfilesFieldNameHelp, Kind: providers.Text, Default: defaultCert, Advanced: true},
		{Key: keyChain, Label: i18n.CertfilesFieldChain, Kind: providers.Text, Default: defaultChain, Advanced: true},
		{Key: keyFullChain, Label: i18n.CertfilesFieldFullchain, Kind: providers.Text, Default: defaultFullChain, Advanced: true},
		{Key: keyKey, Label: i18n.CertfilesFieldKey, Kind: providers.Text, Default: defaultKey, Advanced: true},
		{Key: keyCombined, Label: i18n.CertfilesFieldCombined, Help: i18n.CertfilesFieldCombinedHelp, Kind: providers.Text, Advanced: true},
		{Key: keyPFX, Label: i18n.CertfilesFieldPfx, Help: i18n.CertfilesFieldPfxHelp, Kind: providers.Text, Advanced: true},
		{Key: keyPassword, Label: i18n.CertfilesFieldPfxPassword, Help: i18n.CertfilesFieldPfxPasswordHelp, Kind: providers.Text, Secret: true, Advanced: true},
		{Key: keyCipher, Label: i18n.CertfilesFieldPfxCipher, Kind: providers.Select, Default: cipherModern, Advanced: true, Options: []providers.Option{
			{Value: cipherModern, Label: i18n.CertfilesCipherModern},
			{Value: cipherLegacy, Label: i18n.CertfilesCipherLegacy},
		}},
		{Key: keyCertMode, Label: i18n.CertfilesFieldCertMode, Help: i18n.CertfilesFieldModeHelp, Kind: providers.Text, Default: defaultCertMode, Advanced: true},
		{Key: keyKeyMode, Label: i18n.CertfilesFieldKeyMode, Help: i18n.CertfilesFieldKeyModeHelp, Kind: providers.Text, Default: defaultKeyMode, Advanced: true},
	}
}

// Files are the settings of the files, as an instance has them.
type Files struct {
	cert, chain, fullChain, key string
	combined, pfx               string // "" when not written
	password                    string
	legacy                      bool
	certMode, keyMode           fs.FileMode
}

// Parse reads the settings of the files from those of an instance; those
// left out take the defaults. It fails for good with a name that is no
// file name, a name two files share, or a mode that is no permission, or
// one that lets everyone read the private key.
func Parse(config, secrets map[string]string) (Files, error) {
	value := func(key, def string) string {
		if v := strings.TrimSpace(config[key]); v != "" {
			return v
		}
		return def
	}
	f := Files{
		cert: value(keyCert, defaultCert), chain: value(keyChain, defaultChain),
		fullChain: value(keyFullChain, defaultFullChain), key: value(keyKey, defaultKey),
		combined: value(keyCombined, ""), pfx: value(keyPFX, ""),
		password: secrets[keyPassword], legacy: config[keyCipher] == cipherLegacy,
	}
	seen := map[string]bool{}
	for _, name := range []string{f.cert, f.chain, f.fullChain, f.key, f.combined, f.pfx} {
		if name == "" {
			continue
		}
		if !plainName(name) {
			return Files{}, retry.Permanent(&contracts.Problem{Key: i18n.CertfilesErrorName, Params: map[string]any{"name": name}})
		}
		if seen[name] {
			return Files{}, retry.Permanent(&contracts.Problem{Key: i18n.CertfilesErrorSame, Params: map[string]any{"name": name}})
		}
		seen[name] = true
	}
	var err error
	if f.certMode, err = mode(value(keyCertMode, defaultCertMode)); err != nil {
		return Files{}, err
	}
	if f.keyMode, err = mode(value(keyKeyMode, defaultKeyMode)); err != nil {
		return Files{}, err
	}
	if f.keyMode&0o007 != 0 {
		return Files{}, retry.Permanent(&contracts.Problem{Key: i18n.CertfilesErrorKeyMode, Params: map[string]any{"mode": fmt.Sprintf("%03o", f.keyMode)}})
	}
	return f, nil
}

// plainName reports whether name names a file in a directory: no path,
// and not . or .., once the domain takes its place.
func plainName(name string) bool {
	n := strings.ReplaceAll(name, Placeholder, "example.com")
	return len(n) <= 255 && n != "." && n != ".." && !strings.ContainsAny(n, "/\\\x00")
}

// mode reads a permission in octal, such as 644.
func mode(v string) (fs.FileMode, error) {
	m, err := strconv.ParseUint(v, 8, 32)
	if err != nil || m > 0o777 {
		return 0, retry.Permanent(&contracts.Problem{Key: i18n.CertfilesErrorMode, Params: map[string]any{"mode": v}})
	}
	return fs.FileMode(m), nil
}

// The kinds of files, for the environment of commands.
const (
	KindCert      = "cert"
	KindChain     = "chain"
	KindFullChain = "fullchain"
	KindKey       = "key"
	KindCombined  = "combined"
	KindPFX       = "pfx"
)

// File is a file to write: its name, what it holds and its mode.
type File struct {
	Kind string
	Name string
	Data []byte
	Mode fs.FileMode
	// Secret files hold the private key.
	Secret bool
}

// Make returns the files of a certificate: chain, its leaf first, and its
// private key. The primary domain of domains takes the place of
// Placeholder in their names.
func (f Files) Make(domains []string, chain []*x509.Certificate, key crypto.Signer) ([]File, error) {
	if len(chain) == 0 {
		return nil, errors.New("certfiles: no certificate")
	}
	domain, err := DomainName(domains)
	if err != nil {
		return nil, err
	}
	keyPEM, err := cryptox.EncodeKey(key)
	if err != nil {
		return nil, err
	}
	name := func(n string) string { return strings.ReplaceAll(n, Placeholder, domain) }
	full := cryptox.EncodeCertificates(chain...)
	out := []File{
		{Kind: KindCert, Name: name(f.cert), Data: cryptox.EncodeCertificates(chain[0]), Mode: f.certMode},
		{Kind: KindChain, Name: name(f.chain), Data: cryptox.EncodeCertificates(chain[1:]...), Mode: f.certMode},
		{Kind: KindFullChain, Name: name(f.fullChain), Data: full, Mode: f.certMode},
		{Kind: KindKey, Name: name(f.key), Data: keyPEM, Mode: f.keyMode, Secret: true},
	}
	if f.combined != "" {
		out = append(out, File{Kind: KindCombined, Name: name(f.combined), Data: append(append([]byte{}, full...), keyPEM...), Mode: f.keyMode, Secret: true})
	}
	if f.pfx != "" {
		data, err := cryptox.EncodePFX(chain, key, f.password, f.legacy)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Kind: KindPFX, Name: name(f.pfx), Data: data, Mode: f.keyMode, Secret: true})
	}
	return out, nil
}

// Names returns the names of the files a certificate of domains is written
// as, by kind, as Make names them.
func (f Files) Names(domains []string) (map[string]string, error) {
	domain, err := DomainName(domains)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for kind, n := range map[string]string{
		KindCert: f.cert, KindChain: f.chain, KindFullChain: f.fullChain, KindKey: f.key, KindCombined: f.combined, KindPFX: f.pfx,
	} {
		if n != "" {
			out[kind] = strings.ReplaceAll(n, Placeholder, domain)
		}
	}
	return out, nil
}

// DomainName names files and directories after the primary domain of a
// certificate, the first of domains: in lowercase, *.example.com as
// star.example.com, and characters other than letters, digits, dots and
// hyphens as underscores.
func DomainName(domains []string) (string, error) {
	if len(domains) == 0 {
		return "", errors.New("certfiles: a certificate without domains")
	}
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		}
		return '_'
	}, strings.Replace(strings.ToLower(domains[0]), "*", "star", 1))
	if strings.Trim(name, ".") == "" {
		return "", fmt.Errorf("certfiles: %q makes no file name", domains[0])
	}
	return name, nil
}

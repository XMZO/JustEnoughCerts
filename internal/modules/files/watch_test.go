package files

import (
	"crypto"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// clients is a certbot and an acme.sh, as they keep their certificates.
type clients struct {
	t    *testing.T
	root string
	ca   *cryptoxtest.CA
	now  time.Time
}

func newClients(t *testing.T) *clients {
	return &clients{t: t, root: t.TempDir(), ca: cryptoxtest.NewCA(t), now: time.Now()}
}

func (c *clients) write(rel, data string) {
	c.t.Helper()
	path := filepath.Join(c.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		c.t.Fatal(err)
	}
}

// issue makes a certificate for names that ends after days.
func (c *clients) issue(days int, names ...string) (*x509.Certificate, crypto.Signer) {
	key := cryptoxtest.NewKey(c.t)
	return c.ca.Leaf(c.t, key, c.now.Add(-time.Hour), c.now.Add(time.Duration(days)*24*time.Hour), names...), key
}

// certbot keeps every version in archive/<name>/ and the current one in
// live/<name>/; acme.sh keeps one per directory.
func (c *clients) certbot(n int, leaf *x509.Certificate, key crypto.Signer) {
	suffix := string(rune('0' + n))
	c.write("letsencrypt/archive/example.com/cert"+suffix+".pem", cryptoxtest.CertPEM(leaf))
	c.write("letsencrypt/archive/example.com/chain"+suffix+".pem", cryptoxtest.CertPEM(c.ca.Intermediate))
	c.write("letsencrypt/archive/example.com/fullchain"+suffix+".pem", cryptoxtest.CertPEM(leaf, c.ca.Intermediate))
	c.write("letsencrypt/archive/example.com/privkey"+suffix+".pem", cryptoxtest.KeyPEM(c.t, key))
	c.write("letsencrypt/live/example.com/fullchain.pem", cryptoxtest.CertPEM(leaf, c.ca.Intermediate))
	c.write("letsencrypt/live/example.com/privkey.pem", cryptoxtest.KeyPEM(c.t, key))
	c.write("letsencrypt/live/README", "do not move these files")
}

func (c *clients) acmesh(leaf *x509.Certificate, key crypto.Signer) {
	c.write("acme.sh/other.example_ecc/other.example.cer", cryptoxtest.CertPEM(leaf))
	c.write("acme.sh/other.example_ecc/other.example.key", cryptoxtest.KeyPEM(c.t, key))
	c.write("acme.sh/other.example_ecc/ca.cer", cryptoxtest.CertPEM(c.ca.Intermediate))
	c.write("acme.sh/other.example_ecc/fullchain.cer", cryptoxtest.CertPEM(leaf, c.ca.Intermediate))
	c.write("acme.sh/account.conf", "ACCOUNT_EMAIL='someone@example.com'")
}

func openWatch(t *testing.T, dir string) *watcher {
	t.Helper()
	p, err := watchFactory(t.Context(), providers.Instance{Config: map[string]string{"directory": dir}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*watcher)
}

func problemOf(err error) *contracts.Problem {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p
	}
	return nil
}

func TestWatchCertbot(t *testing.T) {
	c := newClients(t)
	old, oldKey := c.issue(20, "example.com", "www.example.com")
	c.certbot(1, old, oldKey)
	w := openWatch(t, c.root)
	req := contracts.CollectRequest{Domains: []string{"example.com", "www.example.com"}}

	got, err := w.Collect(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cryptox.NewBundle(got.Chain, got.Key)
	if err != nil || !b.Leaf.Equal(old) || len(b.Chain) != 1 {
		t.Fatalf("collected %v, %v", got, err)
	}

	// Nothing new: wait for certbot.
	req.Current, req.CurrentEnd = cryptox.Fingerprint(old), old.NotAfter
	var wait *contracts.Wait
	if _, err := w.Collect(t.Context(), req); !errors.As(err, &wait) || wait.After != recheck {
		t.Fatalf("nothing new: %v", err)
	}

	// certbot renewed: the newest of the archive and live/.
	next, nextKey := c.issue(90, "example.com", "www.example.com")
	c.certbot(2, next, nextKey)
	got, err = w.Collect(t.Context(), req)
	if err != nil || !got.Chain[0].Equal(next) {
		t.Fatalf("renewed: %v, %v", got, err)
	}

	if _, err := w.Collect(t.Context(), contracts.CollectRequest{Domains: []string{"example.com", "other.example.com"}}); problemOf(err) == nil || problemOf(err).Key != i18n.FilesErrorWatchNone {
		t.Errorf("domains it does not hold: %v", err)
	}
}

func TestWatchAcmesh(t *testing.T) {
	c := newClients(t)
	leaf, key := c.issue(60, "other.example")
	c.acmesh(leaf, key)
	cert, certKey := c.issue(90, "example.com")
	c.certbot(1, cert, certKey)

	got, err := openWatch(t, filepath.Join(c.root, "acme.sh")).Collect(t.Context(), contracts.CollectRequest{Domains: []string{"OTHER.example"}})
	if err != nil || !got.Chain[0].Equal(leaf) || len(got.Chain) != 2 {
		t.Fatalf("collected %v, %v", got, err)
	}

	// Both clients under one root, found by the test.
	msg, err := openWatch(t, c.root).Test(t.Context())
	if err != nil || msg.Key != i18n.FilesTestWatchOk || msg.Params["count"] != 2 || msg.Params["domains"] != "example.com, other.example" {
		t.Errorf("test: %+v, %v", msg, err)
	}
	// And listed, to pick from, with when they end.
	held, err := openWatch(t, c.root).Held(t.Context())
	var listed []string
	for _, h := range held {
		listed = append(listed, strings.Join(h.Domains, ",")+" "+h.NotAfter.UTC().Format(time.DateOnly))
	}
	slices.Sort(listed)
	if want := []string{"example.com " + cert.NotAfter.UTC().Format(time.DateOnly), "other.example " + leaf.NotAfter.UTC().Format(time.DateOnly)}; err != nil || !slices.Equal(listed, want) {
		t.Errorf("held %q, want %q: %v", listed, want, err)
	}
}

func TestWatchExpired(t *testing.T) {
	c := newClients(t)
	gone, key := c.issue(-1, "example.com")
	c.certbot(1, gone, key)
	if _, err := openWatch(t, c.root).Collect(t.Context(), contracts.CollectRequest{Domains: []string{"example.com"}}); problemOf(err) == nil || problemOf(err).Key != i18n.FilesErrorWatchNone {
		t.Errorf("an expired one: %v", err)
	}
}

func TestWatchErrors(t *testing.T) {
	c := newClients(t)
	for _, tt := range []struct {
		name      string
		dir       string
		key       i18n.Key
		permanent bool
	}{
		{"relative", "letsencrypt", i18n.FilesErrorRelative, true},
		{"missing", filepath.Join(c.root, "nothing"), i18n.FilesErrorWatchMissing, false},
	} {
		_, err := openWatch(t, tt.dir).Collect(t.Context(), contracts.CollectRequest{Domains: []string{"example.com"}})
		if p := problemOf(err); p == nil || p.Key != tt.key || retry.IsPermanent(err) != tt.permanent {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
	c.write("empty/notes.txt", "nothing here")
	if _, err := openWatch(t, filepath.Join(c.root, "empty")).Test(t.Context()); problemOf(err) == nil || problemOf(err).Key != i18n.FilesErrorWatchEmpty {
		t.Errorf("empty: %v", err)
	}

	// A key only its owner may read.
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	leaf, key := c.issue(90, "example.com")
	c.certbot(1, leaf, key)
	for _, path := range []string{"letsencrypt/live/example.com/privkey.pem", "letsencrypt/archive/example.com/privkey1.pem"} {
		if err := os.Chmod(filepath.Join(c.root, filepath.FromSlash(path)), 0); err != nil {
			t.Fatal(err)
		}
	}
	_, err := openWatch(t, c.root).Collect(t.Context(), contracts.CollectRequest{Domains: []string{"example.com"}})
	if p := problemOf(err); p == nil || p.Key != i18n.FilesErrorWatchDenied || !strings.Contains(p.Params["path"].(string), "privkey") || p.Params["group"] != strconv.Itoa(os.Getgid()) {
		t.Errorf("unreadable: %v", err)
	}
	if _, err := openWatch(t, c.root).Held(t.Context()); problemOf(err) == nil || problemOf(err).Key != i18n.FilesErrorWatchDenied {
		t.Errorf("unreadable, listed: %v", err)
	}
}

func TestWatchLinkOutside(t *testing.T) {
	c := newClients(t)
	leaf, key := c.issue(90, "example.com")
	c.certbot(1, leaf, key)
	// certbot's live/ links into archive/: from live/ alone, the links lead
	// out of the directory, where the scan does not follow.
	live := filepath.Join(c.root, "letsencrypt", "live", "example.com")
	for _, f := range []string{"fullchain.pem", "privkey.pem"} {
		target := filepath.Join("..", "..", "archive", "example.com", strings.Replace(f, ".pem", "1.pem", 1))
		if err := os.Remove(filepath.Join(live, f)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(live, f)); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
	}
	if got, err := openWatch(t, filepath.Join(c.root, "letsencrypt")).Collect(t.Context(), contracts.CollectRequest{Domains: []string{"example.com"}}); err != nil || !got.Chain[0].Equal(leaf) {
		t.Errorf("through the links inside: %v", err)
	}
	_, err := openWatch(t, filepath.Join(c.root, "letsencrypt", "live")).Collect(t.Context(), contracts.CollectRequest{Domains: []string{"example.com"}})
	if p := problemOf(err); p == nil || p.Key != i18n.FilesErrorWatchOutside {
		t.Errorf("links out: %v", err)
	}
}

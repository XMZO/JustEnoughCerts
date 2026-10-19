package files

import (
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

func request(t *testing.T, domains ...string) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), domains...)
	return contracts.DeployRequest{CertificateID: 1, Domains: domains, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func open(t *testing.T, config map[string]string) *directory {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: config})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*directory)
}

func TestDeploy(t *testing.T) {
	root := t.TempDir()
	req := request(t, "*.example.com", "example.com")
	var logged []i18n.Key
	req.Log = func(l contracts.LogLine) { logged = append(logged, l.Key) }
	res, err := open(t, map[string]string{"directory": root, "layout": byDomain}).Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "" || len(res.Removed) != 0 || len(logged) != 1 || logged[0] != i18n.FilesLogWrite {
		t.Errorf("result %+v, log %v", res, logged)
	}
	dir := filepath.Join(root, "star.example.com")
	b, err := cryptox.ParseBundle(read(t, dir, "fullchain.pem"), read(t, dir, "privkey.pem"))
	if err != nil || b.Leaf.Equal(req.Chain[0]) == false || len(b.Chain) != 1 {
		t.Fatalf("files: %v", err)
	}
	if certs, err := cryptox.ParseCertificates(read(t, dir, "cert.pem")); err != nil || len(certs) != 1 {
		t.Errorf("cert.pem: %v", err)
	}
	if certs, err := cryptox.ParseCertificates(read(t, dir, "chain.pem")); err != nil || len(certs) != 1 || !certs[0].Equal(req.Chain[1]) {
		t.Errorf("chain.pem: %v", err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(dir, "privkey.pem")); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("privkey.pem mode %v, %v", info.Mode(), err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 4 {
		t.Errorf("%d files: temporary ones left?", len(entries))
	}

	// A new version replaces the files.
	req2 := request(t, "*.example.com")
	if _, err := open(t, map[string]string{"directory": root, "layout": byDomain}).Deploy(t.Context(), req2); err != nil {
		t.Fatal(err)
	}
	if certs, _ := cryptox.ParseCertificates(read(t, dir, "cert.pem")); !certs[0].Equal(req2.Chain[0]) {
		t.Error("cert.pem was not replaced")
	}
}

func TestDeployFlat(t *testing.T) {
	root := t.TempDir()
	if _, err := open(t, map[string]string{"directory": root, "layout": flat}).Deploy(t.Context(), request(t, "example.com")); err != nil {
		t.Fatal(err)
	}
	read(t, root, "fullchain.pem")
}

func TestDeployErrors(t *testing.T) {
	_, err := open(t, map[string]string{"directory": "certs"}).Deploy(t.Context(), request(t, "example.com"))
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.FilesErrorRelative || !retry.IsPermanent(err) {
		t.Errorf("a relative directory: %v", err)
	}
	// A file where the directory should be.
	root := t.TempDir()
	blocked := filepath.Join(root, "example.com")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = open(t, map[string]string{"directory": root}).Deploy(t.Context(), request(t, "example.com"))
	if !errors.As(err, &p) || p.Key != i18n.FilesErrorWrite {
		t.Errorf("a blocked directory: %v", err)
	}
}

func TestFail(t *testing.T) {
	tests := []struct {
		err       error
		key       i18n.Key
		permanent bool
	}{
		{&os.PathError{Op: "open", Path: "/etc/x", Err: syscall.EROFS}, i18n.FilesErrorReadOnly, true},
		{&os.PathError{Op: "open", Path: "/etc/x", Err: syscall.EACCES}, i18n.FilesErrorWrite, true},
		{&os.PathError{Op: "write", Path: "/srv/x", Err: syscall.ENOSPC}, i18n.FilesErrorWrite, false},
	}
	for _, tt := range tests {
		err := fail("/etc/x", tt.err)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != tt.key || p.Params["directory"] != "/etc/x" || retry.IsPermanent(err) != tt.permanent {
			t.Errorf("fail(%v) = %v, permanent %v; want %s, permanent %v", tt.err, err, retry.IsPermanent(err), tt.key, tt.permanent)
		}
	}
}

func TestDirName(t *testing.T) {
	for in, want := range map[string]string{"*.Example.com": "star.example.com", "xn--bcher-kva.de": "xn--bcher-kva.de", "a/b": "a_b"} {
		if got, err := dirName([]string{in}); err != nil || got != want {
			t.Errorf("dirName(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"..", "."} {
		if _, err := dirName([]string{bad}); err == nil {
			t.Errorf("dirName(%q) made a name", bad)
		}
	}
}

func read(t *testing.T, dir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "certs")
	msg, err := open(t, map[string]string{"directory": dir}).Test(t.Context())
	if err != nil || msg.Key != i18n.FilesTestOk {
		t.Fatalf("test: %+v, %v", msg, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("the directory after the test: %v %v", entries, err)
	}
	var p *contracts.Problem
	if _, err := open(t, map[string]string{"directory": "certs"}).Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.FilesErrorRelative {
		t.Errorf("a relative directory: %v", err)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := open(t, map[string]string{"directory": blocked}).Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.FilesErrorWrite {
		t.Errorf("a file in the way: %v", err)
	}
}

func TestDeployNamedFiles(t *testing.T) {
	root := t.TempDir()
	req := request(t, "*.example.com")
	d := open(t, map[string]string{
		"directory": root, "layout": flat, "cert_file": "{domain}.crt", "key_file": "{domain}.key",
		"combined_file": "{domain}.pem", "pfx_file": "{domain}.pfx", "cert_mode": "640", "key_mode": "640",
		"command": `echo "$JEC_CERT_FILE|$JEC_KEY_FILE|$JEC_COMBINED_FILE|$JEC_PFX_FILE" > env.txt`,
	})
	if _, err := d.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"star.example.com.crt", "chain.pem", "fullchain.pem", "star.example.com.key", "star.example.com.pem", "star.example.com.pfx"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
			t.Errorf("%s: mode %v", name, info.Mode())
		}
	}
	if b, err := cryptox.ParseBundle(read(t, root, "star.example.com.pem"), nil); err != nil || !b.Leaf.Equal(req.Chain[0]) {
		t.Errorf("the one file: %v", err)
	}
	if b, err := cryptox.ParsePFX(read(t, root, "star.example.com.pfx"), ""); err != nil || !b.Leaf.Equal(req.Chain[0]) {
		t.Errorf("the PFX file: %v", err)
	}
	at := func(name string) string { return filepath.Join(root, name) }
	want := at("star.example.com.crt") + "|" + at("star.example.com.key") + "|" + at("star.example.com.pem") + "|" + at("star.example.com.pfx") + "\n"
	if got := string(read(t, root, "env.txt")); got != want {
		t.Errorf("the command found %q", got)
	}
}

func TestDeploySettingsRefused(t *testing.T) {
	for _, c := range []struct {
		name   string
		config map[string]string
		key    i18n.Key
	}{
		{"a name with a path", map[string]string{"cert_file": "../cert.pem"}, i18n.CertfilesErrorName},
		{"a key everyone reads", map[string]string{"key_mode": "644"}, i18n.CertfilesErrorKeyMode},
		{"an owner that is none", map[string]string{"owner": "root:wheel:x"}, i18n.FilesErrorOwner},
		{"an unknown user", map[string]string{"owner": "no-such-user-of-jec"}, i18n.FilesErrorOwnerUnknown},
	} {
		c.config["directory"] = t.TempDir()
		_, err := factory(t.Context(), providers.Instance{Config: c.config})
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestDeployOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no owners to set")
	}
	root := t.TempDir()
	req := request(t, "example.com")
	// The owner JEC runs as, which it may always set.
	me := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	if _, err := open(t, map[string]string{"directory": root, "owner": me}).Deploy(t.Context(), req); err != nil {
		t.Fatalf("owner %s: %v", me, err)
	}
	if os.Getuid() == 0 {
		return // root may give the files to anyone
	}
	_, err := open(t, map[string]string{"directory": root, "owner": "0:0"}).Deploy(t.Context(), req)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.FilesErrorChown || !retry.IsPermanent(err) {
		t.Errorf("another owner: %v", err)
	}
}

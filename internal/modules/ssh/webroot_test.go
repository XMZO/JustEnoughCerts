package ssh

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func openWebroot(t *testing.T, config, secrets map[string]string) *webroot {
	t.Helper()
	p, err := webrootFactory(t.Context(), providers.Instance{Config: config, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	w := p.(*webroot)
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestWebroot(t *testing.T) {
	needShell(t)
	key, pub := clientKey(t, "")
	srv := newServer(t, pub)
	root := t.TempDir()
	w := openWebroot(t, settings(srv, map[string]string{"directory": root, "layout": shared}), map[string]string{"private_key": key})
	f := contracts.File{Domain: "example.com", Path: "/.well-known/acme-challenge/tok-1", Content: []byte("answer")}
	for range 2 { // the second time over the first
		if err := w.PutFile(t.Context(), f); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, ".well-known", "acme-challenge", "tok-1")
	if got, err := os.ReadFile(file); err != nil || string(got) != "answer" {
		t.Fatalf("file %q, %v", got, err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("mode %v, %v", info.Mode(), err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(file)); len(entries) != 1 {
		t.Errorf("%d files: temporary ones left?", len(entries))
	}
	for range 2 { // gone already the second time
		if err := w.RemoveFile(t.Context(), f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("not removed: %v", err)
	}
	if msg, err := w.Test(t.Context()); err != nil || msg.Key != i18n.SshTestWebrootOk {
		t.Errorf("test: %v %v", msg, err)
	}

	// Each domain in a subdirectory, with a password.
	w = openWebroot(t, settings(srv, map[string]string{"directory": root, "layout": perDomain}), map[string]string{"password": password})
	if err := w.PutFile(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "example.com", ".well-known", "acme-challenge", "tok-1")); err != nil {
		t.Errorf("per domain: %v", err)
	}
}

func TestWebrootFailures(t *testing.T) {
	needShell(t)
	key, pub := clientKey(t, "")
	srv := newServer(t, pub)
	root := t.TempDir()
	w := openWebroot(t, settings(srv, map[string]string{"directory": root, "layout": shared}), map[string]string{"private_key": key})

	// Nothing outside /.well-known/, and nothing run for it.
	err := w.PutFile(t.Context(), contracts.File{Domain: "example.com", Path: "/index.html"})
	srv.mu.Lock()
	ran := len(srv.commands)
	srv.mu.Unlock()
	if problemKey(err) != i18n.SshErrorFilePath || !retry.IsPermanent(err) || ran != 0 {
		t.Errorf("outside: %v, %d commands", err, ran)
	}

	// A directory the user may not write to.
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	w = openWebroot(t, settings(srv, map[string]string{"directory": locked, "layout": shared}), map[string]string{"private_key": key})
	err = w.PutFile(t.Context(), contracts.File{Domain: "example.com", Path: "/.well-known/x"})
	if problemKey(err) != i18n.SshErrorWrite || !retry.IsPermanent(err) {
		t.Errorf("locked: %v", err)
	}
}

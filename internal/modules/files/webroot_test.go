package files

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func openWebroot(t *testing.T, config map[string]string) *webroot {
	t.Helper()
	p, err := webrootFactory(t.Context(), providers.Instance{Config: config})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*webroot)
}

func TestWebroot(t *testing.T) {
	root := t.TempDir()
	w := openWebroot(t, map[string]string{"directory": root, "layout": shared})
	f := contracts.File{Domain: "example.com", Path: "/.well-known/pki-validation/ABC.txt", Content: []byte("abc\ncomodoca.com")}
	if err := w.PutFile(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, ".well-known", "pki-validation", "ABC.txt")
	if got, err := os.ReadFile(file); err != nil || string(got) != string(f.Content) {
		t.Fatalf("file %q, %v", got, err)
	}
	if info, err := os.Stat(file); runtime.GOOS != "windows" && (err != nil || info.Mode().Perm() != 0o644) {
		t.Errorf("mode %v, %v", info.Mode(), err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(file)); len(entries) != 1 {
		t.Errorf("%d files: temporary ones left?", len(entries))
	}
	if err := w.RemoveFile(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("not removed: %v", err)
	}
	if err := w.RemoveFile(t.Context(), f); err != nil {
		t.Errorf("removing it again: %v", err)
	}

	// Each domain in a subdirectory.
	w = openWebroot(t, map[string]string{"directory": root, "layout": perDomain})
	if err := w.PutFile(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "example.com", ".well-known", "pki-validation", "ABC.txt")); err != nil {
		t.Errorf("per domain: %v", err)
	}

	if msg, err := w.Test(t.Context()); err != nil || msg.Key != i18n.FilesTestWebrootOk {
		t.Errorf("test: %v %v", msg, err)
	}
}

func TestWebrootRefuses(t *testing.T) {
	root := t.TempDir()
	w := openWebroot(t, map[string]string{"directory": root, "layout": shared})
	for _, p := range []string{
		"/index.html", "/.well-known/", "/.well-known", "/.well-known/../x", "/.well-known/a b", "/.well-known//x",
		".well-known/x", "/.well-known/x/", "/.well-known/./x", "/.well-known/x%2F..",
	} {
		err := w.PutFile(t.Context(), contracts.File{Domain: "example.com", Path: p})
		var problem *contracts.Problem
		if !errors.As(err, &problem) || problem.Key != i18n.FilesErrorFilePath || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("wrote %d entries", len(entries))
	}

	err := openWebroot(t, map[string]string{"directory": "www", "layout": shared}).PutFile(t.Context(),
		contracts.File{Domain: "example.com", Path: "/.well-known/x"})
	var problem *contracts.Problem
	if !errors.As(err, &problem) || problem.Key != i18n.FilesErrorRelative {
		t.Errorf("relative: %v", err)
	}
}

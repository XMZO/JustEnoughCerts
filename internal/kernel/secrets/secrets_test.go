package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKeyFile(t *testing.T) {
	dir := t.TempDir()
	box, created, err := Load(dir, "")
	if err != nil || !created {
		t.Fatalf("first Load: created %v, %v", created, err)
	}
	path := filepath.Join(dir, KeyFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %v, want 0600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data))); err != nil || len(raw) != keySize {
		t.Errorf("key file holds %q", data)
	}

	sealed := box.Seal([]byte("private key"), "certs.version:1:key")
	again, created, err := Load(dir, "")
	if err != nil || created {
		t.Fatalf("second Load: created %v, %v", created, err)
	}
	if got, err := again.Open(sealed, "certs.version:1:key"); err != nil || string(got) != "private key" {
		t.Errorf("Open with the reloaded key = %q, %v", got, err)
	}
}

func TestEnvKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, keySize))
	dir := t.TempDir()
	box, created, err := Load(dir, " "+key+"\n")
	if err != nil || created {
		t.Fatalf("Load with a key: created %v, %v", created, err)
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFile)); !os.IsNotExist(err) {
		t.Error("a key file was written although JEC_SECRET_KEY is set")
	}
	other, _, err := Load(t.TempDir(), key)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := other.Open(box.Seal([]byte("x"), "c"), "c"); err != nil || string(got) != "x" {
		t.Errorf("the same key does not open: %q, %v", got, err)
	}
	for _, bad := range []string{"not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, _, err := Load(dir, bad); err == nil || !strings.Contains(err.Error(), "JEC_SECRET_KEY") {
			t.Errorf("Load(%q) = %v", bad, err)
		}
	}
}

func TestBadKeyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, KeyFile), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(dir, ""); err == nil || !strings.Contains(err.Error(), KeyFile) {
		t.Errorf("Load with a broken key file = %v", err)
	}
}

func TestSealOpen(t *testing.T) {
	box, _, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	sealed := box.Seal([]byte("token"), "dnspod.credential:3")
	if bytes.Contains(sealed, []byte("token")) {
		t.Error("the plaintext shows in the sealed data")
	}
	if bytes.Equal(sealed, box.Seal([]byte("token"), "dnspod.credential:3")) {
		t.Error("sealing twice gave the same bytes")
	}
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1
	for name, open := range map[string]func() ([]byte, error){
		"other context": func() ([]byte, error) { return box.Open(sealed, "dnspod.credential:4") },
		"other key":     func() ([]byte, error) { return other.Open(sealed, "dnspod.credential:3") },
		"tampered":      func() ([]byte, error) { return box.Open(tampered, "dnspod.credential:3") },
		"truncated":     func() ([]byte, error) { return box.Open(sealed[:10], "dnspod.credential:3") },
		"unknown version": func() ([]byte, error) {
			v := bytes.Clone(sealed)
			v[0] = 9
			return box.Open(v, "dnspod.credential:3")
		},
	} {
		if _, err := open(); !errors.Is(err, ErrOpen) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, err := box.Open(box.Seal(nil, "c"), "c"); err != nil || len(got) != 0 {
		t.Errorf("empty plaintext: %q, %v", got, err)
	}
}

func TestScope(t *testing.T) {
	box, _, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	certs, dns := box.Scope("certs"), box.Scope("dnspod")
	sealed := certs.Seal([]byte("key"), "version:1")
	if got, err := certs.Open(sealed, "version:1"); err != nil || string(got) != "key" {
		t.Errorf("Open in the same module = %q, %v", got, err)
	}
	if _, err := dns.Open(sealed, "version:1"); !errors.Is(err, ErrOpen) {
		t.Errorf("another module opened the data: %v", err)
	}
}

func TestCheck(t *testing.T) {
	box, _, _ := Load(t.TempDir(), "")
	other, _, _ := Load(t.TempDir(), "")
	check := box.Check()
	if !box.Verify(check) || other.Verify(check) || box.Verify([]byte("junk")) {
		t.Error("Verify does not tell the keys apart")
	}
}

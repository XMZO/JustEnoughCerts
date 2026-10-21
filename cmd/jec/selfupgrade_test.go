package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// releaseServer serves a release as GitHub does: its checksums and its
// archive for this platform, whose jec is a script that tells version.
func releaseServer(t *testing.T, version string, sums func(name, sum string) string) {
	t.Helper()
	script := []byte("#!/bin/sh\necho 'jec " + version + "'\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
	}{{"LICENSE", []byte("MIT")}, {"jec", script}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	name := archive(version)
	sum := sha256.Sum256(buf.Bytes())
	mux := http.NewServeMux()
	mux.HandleFunc("/v"+version+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sums(name, hex.EncodeToString(sum[:]))))
	})
	mux.HandleFunc("/v"+version+"/"+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(buf.Bytes()) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = old })
}

// fakeBinary stands in for the running binary.
func fakeBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "jec")
	if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := executable
	executable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { executable = old })
	return bin
}

func TestUpgrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the release's jec is a shell script here")
	}
	releaseServer(t, "9.9.9", func(name, sum string) string { return sum + "  other.tar.gz\n" + sum + "  " + name + "\n" })
	bin := fakeBinary(t)
	oldDocker := dockerEnv
	dockerEnv = filepath.Join(t.TempDir(), "none")
	oldUnit := unitFile
	unitFile = filepath.Join(t.TempDir(), "none.service")
	t.Cleanup(func() { dockerEnv, unitFile = oldDocker, oldUnit })

	var out, errs bytes.Buffer
	if code := run(t.Context(), []string{"upgrade", "-version", "v9.9.9"}, &out, &errs); code != exitOK {
		t.Fatalf("exit %d: %s %s", code, out.String(), errs.String())
	}
	data, err := os.ReadFile(bin)
	if err != nil || !strings.Contains(string(data), "jec 9.9.9") || !strings.Contains(out.String(), "is now jec 9.9.9") {
		t.Errorf("the binary holds %q; said %s", data, out.String())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(bin), ".jec-upgrade-*")); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}

	// In a container: the image is pulled instead.
	if err := os.WriteFile(dockerEnv, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	errs.Reset()
	if code := run(t.Context(), []string{"upgrade", "-version", "9.9.9"}, &out, &errs); code == exitOK || !strings.Contains(errs.String(), "container") {
		t.Errorf("in a container: %d %s", code, errs.String())
	}
}

func TestUpgradeChecksum(t *testing.T) {
	releaseServer(t, "9.9.9", func(name, _ string) string { return strings.Repeat("0", 64) + "  " + name + "\n" })
	bin := fakeBinary(t)
	u := upgrader{client: http.DefaultClient, out: &bytes.Buffer{}, bin: bin, current: "dev"}
	if err := u.upgrade(t.Context(), "9.9.9"); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Errorf("a mismatched archive: %v", err)
	}
	if data, _ := os.ReadFile(bin); string(data) != "old" {
		t.Errorf("the binary changed: %q", data)
	}
	// A release without this platform's archive.
	releaseServer(t, "9.9.8", func(string, string) string { return "" })
	if err := u.upgrade(t.Context(), "9.9.8"); err == nil || !strings.Contains(err.Error(), "for this platform") {
		t.Errorf("no archive: %v", err)
	}
}

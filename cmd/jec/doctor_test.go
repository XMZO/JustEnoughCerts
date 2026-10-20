package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

func TestDoctorLocal(t *testing.T) {
	clearEnv(t)
	t.Setenv("JEC_SECRET_KEY", "")
	t.Setenv("JEC_DATABASE_URL", "")
	if store.TestingPostgres() {
		t.Skip("doctor looks for the SQLite file")
	}
	data := t.TempDir()
	doctor := func() (int, string) {
		t.Helper()
		code, stdout, stderr := runCLI(t.Context(), "doctor", "--offline", "--data-dir", data)
		return code, stdout + stderr
	}

	// Nothing yet, and doctor makes nothing.
	if code, out := doctor(); code != exitOK || strings.Count(out, "none yet") != 2 {
		t.Errorf("a new data directory: exit %d\n%s", code, out)
	}
	for _, name := range []string{store.FileName, secrets.KeyFile} {
		if _, err := os.Stat(filepath.Join(data, name)); !os.IsNotExist(err) {
			t.Errorf("doctor made %s: %v", name, err)
		}
	}

	// A database without tables, as a new PostgreSQL one is.
	st, err := store.Open(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if code, out := doctor(); code != exitOK || !strings.Contains(out, "empty") {
		t.Errorf("an empty database: exit %d\n%s", code, out)
	}

	cfg := config.Defaults()
	cfg.DataDir = data
	app, err := kernel.New(t.Context(), cfg, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	if code, out := doctor(); code != exitOK || !strings.Contains(out, "0 account(s)") || !strings.Contains(out, "opens the secrets") {
		t.Errorf("a data directory in use: exit %d\n%s", code, out)
	}

	// Another key than the one the database was sealed with.
	key := filepath.Join(data, secrets.KeyFile)
	right, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if _, _, err := secrets.Load(other, ""); err != nil {
		t.Fatal(err)
	}
	wrong, err := os.ReadFile(filepath.Join(other, secrets.KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, wrong, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := doctor(); code != exitError || !strings.Contains(out, "not the one") {
		t.Errorf("a wrong key: exit %d\n%s", code, out)
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if code, out := doctor(); code != exitError || !strings.Contains(out, "is missing") {
		t.Errorf("a lost key: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(key); !os.IsNotExist(err) {
		t.Errorf("doctor made a key: %v", err)
	}
	if err := os.WriteFile(key, right, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, store.RestoreFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := doctor(); code != exitOK || !strings.Contains(out, "waits") {
		t.Errorf("the key back, a restore waiting: exit %d\n%s", code, out)
	}
}

func TestDoctorNetwork(t *testing.T) {
	clearEnv(t)
	t.Setenv("JEC_SECRET_KEY", "")
	t.Setenv("JEC_DATABASE_URL", "")
	var date string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/directory" {
			http.NotFound(w, r)
			return
		}
		if date != "" {
			w.Header().Set("Date", date)
		}
		_, _ = w.Write([]byte(`{"newNonce": "x"}`))
	}))
	defer srv.Close()
	saved := doctorClient
	doctorClient = srv.Client()
	defer func() { doctorClient = saved }()

	for _, tt := range []struct {
		name, path, date string
		code             int
		want             string
	}{
		{"in time", "/directory", "", exitOK, "answered in"},
		{"ahead", "/directory", time.Now().Add(-2 * time.Minute).UTC().Format(http.TimeFormat), exitOK, "ahead of the CA"},
		{"far behind", "/directory", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), exitError, "behind the CA"},
		{"no directory", "/nothing", "", exitError, "404"},
	} {
		date = tt.date
		code, stdout, stderr := runCLI(t.Context(), "doctor", "--data-dir", t.TempDir(), "--ca", srv.URL+tt.path)
		if code != tt.code || !strings.Contains(stdout, tt.want) {
			t.Errorf("%s: exit %d\n%s%s", tt.name, code, stdout, stderr)
		}
	}
}

func TestDoctorClock(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		date string
		mark string
	}{
		{"", "[--  ]"},
		{now.Add(-10 * time.Second).Format(http.TimeFormat), "[ok  ]"},
		{now.Add(time.Minute).Format(http.TimeFormat), "[WARN]"},
		{now.Add(-10 * time.Minute).Format(http.TimeFormat), "[FAIL]"},
	} {
		var out bytes.Buffer
		d := &doctor{out: &out}
		d.clock(tt.date, now)
		if !strings.HasPrefix(out.String(), tt.mark) || d.failed != (tt.mark == "[FAIL]") {
			t.Errorf("Date %q: %s (failed %v)", tt.date, out.String(), d.failed)
		}
	}
}

func TestDoctorTLS(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	ca := cryptoxtest.NewCAFrom(t, now.Add(-24*time.Hour))
	key := cryptoxtest.NewKey(t)
	dir := t.TempDir()
	write := func(name, data string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	keyFile := write("key.pem", cryptoxtest.KeyPEM(t, key))
	good := write("good.pem", cryptoxtest.CertPEM(ca.Leaf(t, key, now.Add(-time.Hour), now.Add(60*24*time.Hour), "jec.example.com")))
	soon := write("soon.pem", cryptoxtest.CertPEM(ca.Leaf(t, key, now.Add(-time.Hour), now.Add(3*24*time.Hour), "jec.example.com")))
	old := write("old.pem", cryptoxtest.CertPEM(ca.Leaf(t, key, now.Add(-2*time.Hour), now.Add(-time.Hour), "jec.example.com")))
	other := write("other.pem", cryptoxtest.CertPEM(ca.Leaf(t, cryptoxtest.NewKey(t), now.Add(-time.Hour), now.Add(60*24*time.Hour), "jec.example.com")))
	for _, tt := range []struct {
		cert, mark string
	}{
		{good, "[ok  ]"},
		{soon, "[WARN]"},
		{old, "[FAIL]"},
		{other, "[FAIL]"},
		{filepath.Join(dir, "missing.pem"), "[WARN]"},
	} {
		var out bytes.Buffer
		d := &doctor{out: &out}
		d.tls(tt.cert, keyFile, now)
		if !strings.HasPrefix(out.String(), tt.mark) || d.failed != (tt.mark == "[FAIL]") {
			t.Errorf("%s: %s", filepath.Base(tt.cert), out.String())
		}
	}
}

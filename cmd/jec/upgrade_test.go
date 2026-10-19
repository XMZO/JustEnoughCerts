package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

// TestUpgradeFromAlpha5 starts JEC on the data of 0.1.0-alpha.5, as one
// upgrading does. testdata/alpha5 is a backup of that release's image
// (jec.db) with its master key, of an instance set up through its API:
// renewal settings and a time zone, a credential, five providers, three
// certificates (one with two versions deployed to a local directory, one
// whose issuance failed), and an API token. The migrations since run, and
// everything reads as before; the sealed secrets still open. The secrets
// are made up for the test.
func TestUpgradeFromAlpha5(t *testing.T) {
	data := t.TempDir()
	for name, mode := range map[string]os.FileMode{store.FileName: 0o600, secrets.KeyFile: 0o600} {
		b, err := os.ReadFile(filepath.Join("testdata", "alpha5", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, name), b, mode); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults()
	cfg.DataDir = data
	// On PostgreSQL, the data goes in as jec import-sqlite takes it.
	if store.TestingPostgres() {
		st, err := store.Open(t.Context(), data)
		if err != nil {
			t.Fatal(err)
		}
		err = kernel.ImportSQLite(t.Context(), filepath.Join(data, store.FileName), st, slog.New(slog.DiscardHandler), modules, false)
		if err = errors.Join(err, st.Close()); err != nil {
			t.Fatal(err)
		}
	}
	app, err := kernel.New(t.Context(), cfg, slog.New(slog.DiscardHandler), modules)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	s := &session{t: t, srv: httptest.NewServer(app.Handler())}
	t.Cleanup(s.srv.Close)
	if status := s.call(http.MethodPost, "/api/v1/session", `{"username": "admin", "password": "correct horse"}`, nil); status != http.StatusOK {
		t.Fatalf("sign in: %d", status)
	}

	var settings struct {
		RenewBeforeDays int    `json:"renewBeforeDays"`
		KeyType         string `json:"keyType"`
		CheckTime       string `json:"checkTime"`
		TimeZone        string `json:"timeZone"`
		Retries         int    `json:"retries"`
	}
	if status := s.call(http.MethodGet, "/api/v1/certs/settings", "", &settings); status != http.StatusOK ||
		settings.RenewBeforeDays != 25 || settings.KeyType != "ecdsa-p256" || settings.CheckTime != "03:30" ||
		settings.TimeZone != "Asia/Shanghai" || settings.Retries != 5 {
		t.Errorf("settings: %d %+v", status, settings)
	}

	var list struct {
		Certificates []struct {
			ID      int64    `json:"id"`
			Domains []string `json:"domains"`
		} `json:"certificates"`
	}
	if status := s.call(http.MethodGet, "/api/v1/certs/certificates", "", &list); status != http.StatusOK {
		t.Fatalf("certificates: %d", status)
	}
	var domains []string
	for _, c := range list.Certificates {
		domains = append(domains, strings.Join(c.Domains, " "))
	}
	slices.Sort(domains)
	if want := []string{"a.example.com www.a.example.com", "b.example.net", "issued.example.com *.issued.example.com"}; !slices.Equal(domains, want) {
		t.Errorf("certificates: %q, want %q", domains, want)
	}
	var detail struct {
		Versions []struct {
			ID int64 `json:"id"`
		} `json:"versions"`
	}
	if status := s.call(http.MethodGet, "/api/v1/certs/certificates/1", "", &detail); status != http.StatusOK || len(detail.Versions) != 2 {
		t.Errorf("certificate 1: %d, %d versions", status, len(detail.Versions))
	}
	var runs struct {
		Runs []struct {
			Kind string `json:"kind"`
		} `json:"runs"`
	}
	if status := s.call(http.MethodGet, "/api/v1/certs/runs?days=0", "", &runs); status != http.StatusOK || len(runs.Runs) != 3 {
		t.Errorf("runs: %d %+v", status, runs.Runs)
	}

	var providers struct {
		Providers []struct {
			Name         string `json:"name"`
			CredentialID int64  `json:"credentialId"`
		} `json:"providers"`
	}
	if status := s.call(http.MethodGet, "/api/v1/providers", "", &providers); status != http.StatusOK || len(providers.Providers) != 5 {
		t.Errorf("providers: %d %+v", status, providers.Providers)
	}
	var tokens struct {
		Tokens []struct {
			Name string `json:"name"`
		} `json:"tokens"`
	}
	if status := s.call(http.MethodGet, "/api/v1/account/tokens", "", &tokens); status != http.StatusOK || len(tokens.Tokens) != 1 || tokens.Tokens[0].Name != "ci" {
		t.Errorf("tokens: %d %+v", status, tokens.Tokens)
	}

	// The export with the secrets opens each of them: the credential's
	// and the Telegram bot's.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.srv.URL+"/api/v1/config/export", strings.NewReader(`{"password": "correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: s.cookie})
	resp, err := s.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	file, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"access_key_secret: fixture-secret", "token: 123:fixture", "time_zone: Asia/Shanghai"} {
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(file), want) {
			t.Errorf("export with secrets: %d, lacks %q:\n%s", resp.StatusCode, want, file)
		}
	}
}

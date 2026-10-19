package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
)

// TestPrometheus scrapes the metrics as Prometheus does: with an API token
// that may only read.
func TestPrometheus(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	var logs logBuffer
	app, err := kernel.New(t.Context(), cfg, slog.New(slog.NewTextHandler(&logs, nil)), modules)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	s := &session{t: t, srv: httptest.NewServer(app.Handler())}
	t.Cleanup(s.srv.Close)
	setup := regexp.MustCompile(`setup_token=(\w+)`).FindStringSubmatch(logs.String())
	if setup == nil {
		t.Fatal("no setup token in the log")
	}
	if status := s.call(http.MethodPost, "/api/v1/setup", `{"token": "`+setup[1]+`", "username": "admin", "password": "correct horse"}`, nil); status != http.StatusCreated {
		t.Fatalf("setup: %d", status)
	}
	var token struct {
		Token string `json:"token"`
	}
	if status := s.call(http.MethodPost, "/api/v1/account/tokens", `{"name": "prometheus", "scope": "read", "password": "correct horse"}`, &token); status != http.StatusCreated {
		t.Fatalf("token: %d", status)
	}

	scrape := func(bearer string) (int, string, string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.srv.URL+"/api/v1/metrics/prometheus", nil)
		if err != nil {
			t.Fatal(err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
	}
	status, kind, body := scrape(token.Token)
	if status != http.StatusOK || !strings.HasPrefix(kind, "text/plain; version=0.0.4") ||
		!strings.Contains(body, "jec_build_info{version=") || !strings.Contains(body, `jec_certificates{state="ok"} 0`) {
		t.Errorf("scrape = %d %s:\n%s", status, kind, body)
	}
	if status, _, _ := scrape(""); status != http.StatusUnauthorized {
		t.Errorf("scrape without a token = %d", status)
	}
}

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
)

// runWithStdin runs jec with input on its standard input, as from a pipe.
func runWithStdin(t *testing.T, input string, args ...string) (int, string, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, input); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	saved := stdin
	stdin = r
	defer func() { stdin = saved; _ = r.Close() }()
	return runCLI(t.Context(), args...)
}

var setupToken = regexp.MustCompile(`setup_token=(\w+)`)

// post sends a JSON request to a fresh kernel on cfg's data directory.
func post(t *testing.T, cfg config.Config, path, body string) (int, string) {
	t.Helper()
	var logs bytes.Buffer
	app, err := kernel.New(context.Background(), cfg, slog.New(slog.NewTextHandler(&logs, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if m := setupToken.FindStringSubmatch(logs.String()); m != nil {
		body = strings.ReplaceAll(body, "$TOKEN", m[1])
	}
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func TestResetPassword(t *testing.T) {
	clearEnv(t)
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	dir := []string{"--data-dir", cfg.DataDir}

	if code, _, stderr := runWithStdin(t, "brand new password\n", append([]string{"reset-password"}, dir...)...); code != exitError || !strings.Contains(stderr, "no account yet") {
		t.Errorf("without an account: exit %d, %s", code, stderr)
	}
	if status, body := post(t, cfg, "/api/v1/setup", `{"token": "$TOKEN", "username": "admin", "password": "correct horse"}`); status != http.StatusCreated {
		t.Fatalf("setup = %d %s", status, body)
	}

	for _, tt := range []struct {
		input string
		args  []string
		want  string
	}{
		{"short\n", nil, "characters"},
		{"brand new password\n", []string{"--username", "nobody"}, "no such account"},
	} {
		args := append(append([]string{"reset-password"}, dir...), tt.args...)
		if code, _, stderr := runWithStdin(t, tt.input, args...); code != exitError || !strings.Contains(stderr, tt.want) {
			t.Errorf("reset-password %v: exit %d, %s", tt.args, code, stderr)
		}
	}

	code, stdout, stderr := runWithStdin(t, "brand new password\r\n", append([]string{"reset-password"}, dir...)...)
	if code != exitOK || !strings.Contains(stdout, "admin") {
		t.Fatalf("reset-password: exit %d, %s %s", code, stdout, stderr)
	}
	if status, _ := post(t, cfg, "/api/v1/session", `{"username": "admin", "password": "correct horse"}`); status != http.StatusUnauthorized {
		t.Errorf("the old password still works: %d", status)
	}
	if status, body := post(t, cfg, "/api/v1/session", `{"username": "admin", "password": "brand new password"}`); status != http.StatusOK {
		t.Errorf("the new password fails: %d %s", status, body)
	}
}

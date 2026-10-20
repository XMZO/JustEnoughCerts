package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
)

var discard = slog.New(slog.DiscardHandler)

// teapot stands in for the app behind the health check.
var teapot = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

func TestHealth(t *testing.T) {
	cfg := config.Defaults()
	cfg.BaseURL = "https://example.com/certs"
	srv := httptest.NewServer(newServer(t, cfg).Handler())
	defer srv.Close()

	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodHead, "/healthz", http.StatusOK},
		{http.MethodGet, "/certs/healthz", http.StatusOK},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodGet, "/nope", http.StatusTeapot},
		{http.MethodPost, "/certs/api/v1/x", http.StatusTeapot},
	}
	for _, tt := range tests {
		req, err := http.NewRequestWithContext(t.Context(), tt.method, srv.URL+tt.path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.method, tt.path, resp.StatusCode, tt.want)
		}
		if tt.want == http.StatusOK && tt.method == http.MethodGet && string(body) != "ok\n" {
			t.Errorf("%s %s body = %q", tt.method, tt.path, body)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	for _, hsts := range []string{"false", "true"} {
		cfg := config.Defaults()
		cfg.HSTS = hsts
		srv := httptest.NewServer(newServer(t, cfg).Handler())
		for _, path := range []string{"/healthz", "/", "/api/v1/manifest"} {
			resp, err := srv.Client().Get(srv.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			h := resp.Header
			if !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") || h.Get("X-Content-Type-Options") != "nosniff" ||
				h.Get("X-Frame-Options") != "DENY" || h.Get("Referrer-Policy") != "same-origin" {
				t.Errorf("%s: headers %v", path, h)
			}
			if got := h.Get("Strict-Transport-Security"); (got != "") != (hsts == "true") {
				t.Errorf("hsts %s, %s: %q", hsts, path, got)
			}
		}
		srv.Close()
	}
}

func TestServeStopsOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	s := newServer(t, config.Defaults())
	go func() { done <- s.Serve(ctx, ln) }()

	url := "http://" + ln.Addr().String() + HealthPath
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() = %v", err)
		}
	case <-time.After(shutdownTimeout + time.Second):
		t.Fatal("Serve did not return after cancel")
	}
	if _, err := net.Dial("tcp", ln.Addr().String()); err == nil {
		t.Error("listener still accepts connections")
	}
}

func TestServeReturnsListenerError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	err = newServer(t, config.Defaults()).Serve(t.Context(), ln)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve() on a closed listener = %v, want an accept error", err)
	}
}

func newServer(t *testing.T, cfg config.Config) *Server {
	t.Helper()
	s, err := New(cfg, discard, teapot)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

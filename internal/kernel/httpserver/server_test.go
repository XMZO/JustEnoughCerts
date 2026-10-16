package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
)

var discard = slog.New(slog.DiscardHandler)

func TestHealth(t *testing.T) {
	cfg := config.Defaults()
	cfg.BaseURL = "https://example.com/certs"
	srv := httptest.NewServer(New(cfg, discard).Handler())
	defer srv.Close()

	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodHead, "/healthz", http.StatusOK},
		{http.MethodGet, "/certs/healthz", http.StatusOK},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodGet, "/nope", http.StatusNotFound},
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

func TestServeStopsOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- New(config.Defaults(), discard).Serve(ctx, ln) }()

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
	err = New(config.Defaults(), discard).Serve(t.Context(), ln)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve() on a closed listener = %v, want an accept error", err)
	}
}

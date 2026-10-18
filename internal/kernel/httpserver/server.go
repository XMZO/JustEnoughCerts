// Package httpserver serves the API and the web UI over HTTP.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
)

// HealthPath answers 200 OK while the server is up. It is mounted at the
// listener root, which is what "jec healthcheck" probes, and also under the
// base path so that monitors can reach it through a reverse proxy.
const HealthPath = "/healthz"

// shutdownTimeout bounds how long in-flight requests get to finish once the
// server is asked to stop.
const shutdownTimeout = 10 * time.Second

// Server is the HTTP front of the process.
type Server struct {
	handler http.Handler
	log     *slog.Logger
}

// New builds the server for cfg; app serves every request but the health
// check.
func New(cfg config.Config, log *slog.Logger, app http.Handler) *Server {
	r := chi.NewRouter()
	paths := []string{HealthPath}
	if base := cfg.BasePath(); base != "" {
		paths = append(paths, base+HealthPath)
	}
	for _, p := range paths {
		r.HandleFunc(p, health)
	}
	r.Handle("/*", app)
	return &Server{handler: secure(r, cfg.HSTS == "true"), log: log}
}

// headers are the security headers of every answer (PLAN.md 10): the UI
// loads nothing from elsewhere and runs no inline script, its only images
// of data are the QR codes of two-factor authentication, and no other site
// may frame it, sniff types in it or learn where its links come from.
var headers = map[string]string{
	"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
		"connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'",
	"X-Content-Type-Options":       "nosniff",
	"X-Frame-Options":              "DENY",
	"Referrer-Policy":              "same-origin",
	"Cross-Origin-Opener-Policy":   "same-origin",
	"Cross-Origin-Resource-Policy": "same-origin",
}

// secure sets the security headers on every answer of next, and with hsts
// Strict-Transport-Security, which browsers heed over HTTPS only, for a
// year.
func secure(next http.Handler, hsts bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		for k, v := range headers {
			h.Set(k, v)
		}
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Serve accepts connections on ln until ctx is done, then shuts down
// gracefully.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// health owns its path for every method, so nothing behind it can shadow
// the check.
func health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "ok\n")
}

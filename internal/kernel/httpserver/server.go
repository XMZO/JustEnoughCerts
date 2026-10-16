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

// New builds the server for cfg.
func New(cfg config.Config, log *slog.Logger) *Server {
	r := chi.NewRouter()
	paths := []string{HealthPath}
	if base := cfg.BasePath(); base != "" {
		paths = append(paths, base+HealthPath)
	}
	for _, p := range paths {
		r.Get(p, health)
		r.Head(p, health)
	}
	return &Server{handler: r, log: log}
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

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "ok\n")
}

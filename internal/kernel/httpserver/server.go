// Package httpserver serves the API and the web UI over HTTP, or HTTPS.
package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
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
	tls     *tls.Config // HTTPS, when set
	// plain answers HTTP beside HTTPS: /.well-known/ and the health check
	// as handler does, the rest sent to HTTPS.
	plain http.Handler
}

// New builds the server for cfg; app serves every request but the health
// check. It fails when cfg names TLS files that do not load.
func New(cfg config.Config, log *slog.Logger, app http.Handler) (*Server, error) {
	r := chi.NewRouter()
	paths := []string{HealthPath}
	if base := cfg.BasePath(); base != "" {
		paths = append(paths, base+HealthPath)
	}
	for _, p := range paths {
		r.HandleFunc(p, health)
	}
	r.Handle("/*", app)
	s := &Server{handler: secure(r, cfg.HSTS == "true"), log: log}
	if cfg.TLS() {
		var err error
		if s.tls, err = TLSConfig(cfg.TLSCert, cfg.TLSKey, log); err != nil {
			return nil, err
		}
		s.plain = toHTTPS(s.handler, cfg.BaseURL)
	}
	return s, nil
}

// toHTTPS answers plain HTTP: the requests under /.well-known/, which CAs
// make over HTTP, and the health check go to next; the rest is sent to the
// same place over HTTPS, at the host of baseURL when it is set.
func toHTTPS(next http.Handler, baseURL string) http.Handler {
	host := ""
	if u, err := url.Parse(baseURL); err == nil && u.Scheme == "https" {
		host = u.Host
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/") || r.URL.Path == HealthPath {
			next.ServeHTTP(w, r)
			return
		}
		to := host
		if to == "" {
			to = r.Host
			if h, _, err := net.SplitHostPort(r.Host); err == nil {
				to = h
				if strings.Contains(h, ":") {
					to = "[" + h + "]"
				}
			}
		}
		if to == "" {
			http.Error(w, "use HTTPS", http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "https://"+to+r.URL.RequestURI(), http.StatusPermanentRedirect) //nolint:gosec // G710: to the host the client asked for, or the base URL's
	})
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
	return s.serve(ctx, ln, s.handler, s.tls)
}

// ServePlain answers plain HTTP on ln beside HTTPS, as Serve does: the
// requests under /.well-known/ and the health check, and the rest sent to
// HTTPS. It is for a server that serves HTTPS only.
func (s *Server) ServePlain(ctx context.Context, ln net.Listener) error {
	if s.plain == nil {
		return errors.New("http: plain HTTP is only beside HTTPS")
	}
	return s.serve(ctx, ln, s.plain, nil)
}

func (s *Server) serve(ctx context.Context, ln net.Listener, handler http.Handler, tlsConfig *tls.Config) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	served := make(chan error, 1)
	if tlsConfig != nil {
		srv.TLSConfig = tlsConfig
		go func() { served <- srv.ServeTLS(ln, "", "") }()
	} else {
		go func() { served <- srv.Serve(ln) }()
	}

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

// Package acme is the ACME module (PLAN.md 5.5): accounts at any
// certificate authority that speaks ACME (RFC 8555), as provider instances
// of kind source, which issue certificates through lego while a solver
// proves control of the domains.
//
// An account is registered the first time it is used; its key is sealed in
// the module's table, keyed by the provider instance. A new private key
// comes with every certificate, made by the caller.
package acme

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	legolog "github.com/go-acme/lego/v5/log"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the ACME module.
type Module struct {
	now func() time.Time
	// roots replaces the system's CA roots for talking to the CA, and
	// skipPropagation trusts the validation records to show without
	// looking: for tests against Pebble, a CA with a private root that
	// need not see real DNS records.
	roots           *x509.CertPool
	skipPropagation bool

	db      *sql.DB
	secrets *secrets.Scope
	log     *slog.Logger
}

// New returns the module.
func New() *Module { return &Module{now: time.Now} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "acme",
		Category:    module.Source,
		Name:        i18n.AcmeModuleName,
		Description: i18n.AcmeModuleDescription,
	}
}

// Migrations returns the module's migrations.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Register adds the account type. lego logs through the module's logger.
func (m *Module) Register(r *module.Registry) error {
	m.db, m.secrets, m.log = r.DB, r.Secrets, r.Log
	legolog.SetDefault(r.Log.With("lib", "lego"))
	return r.Providers.Add(accountType(), m.factory)
}

// client is the HTTP client lego talks to the CA with, through the proxy
// of the account, if any, or else that of the environment; lego wraps its
// transport, so it gets one of its own.
func (m *Module) client(proxy string) (*http.Client, error) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: m.roots, MinVersion: tls.VersionTLS12}
	if proxy = strings.TrimSpace(proxy); proxy != "" {
		u, err := httpx.ParseProxy(proxy)
		if err != nil {
			return nil, httpx.ProxyProblem(err)
		}
		t.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: t, Timeout: time.Minute}, nil
}

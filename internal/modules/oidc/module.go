// Package oidc is the module of single sign-on: people sign in to JEC
// through an OpenID Connect provider, such as Authelia, Authentik,
// Keycloak or Google, with the authorization code flow and PKCE, and
// whoever the provider vouches for, if listed among those allowed, gets a
// session of JEC's account (module.Registry.StartSession). Signing in with
// the password stays as it is.
package oidc

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the oidc module.
type Module struct {
	db           *sql.DB
	secrets      *secrets.Scope
	log          *slog.Logger
	startSession func(context.Context) (http.Cookie, error)
	// client reaches the provider, which may be on the local network, as
	// one people run themselves; now is the time; tests replace them.
	client *http.Client
	now    func() time.Time

	mu     sync.Mutex
	cached *provider // the provider of the settings, as last discovered
}

// provider is what JEC knows of the provider of an issuer: its discovery
// document, when read, and its keys, once fetched.
type provider struct {
	issuer string
	d      discovery
	at     time.Time
	keys   *jose.JSONWebKeySet
}

// New returns the module.
func New() *Module {
	client, _ := httpx.Client(httpx.Options{Timeout: 15 * time.Second})
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Module{client: client, now: time.Now}
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "oidc",
		Category:    module.Feature,
		Name:        i18n.OidcModuleName,
		Description: i18n.OidcModuleDescription,
	}
}

// Migrations returns the module's migrations.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Register adds the settings, the status the sign-in page asks for, and
// the sign-in and its return from the provider.
func (m *Module) Register(r *module.Registry) error {
	m.db, m.secrets, m.log, m.startSession = r.DB, r.Secrets, r.Log, r.StartSession
	m.register(r.API)
	return nil
}

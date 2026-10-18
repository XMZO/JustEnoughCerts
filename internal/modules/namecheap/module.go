// Package namecheap is the module of Namecheap's SSL Manager (PLAN.md 5.6):
// JEC acts as the server-side client Namecheap gives its customers, to get
// each certificate of a subscription, such as a PositiveSSL every 200
// days, and deploy it wherever JEC does.
//
// A provider instance registers with the one-time token from Namecheap's
// panel, the first time it is used or tested; the access token that comes
// back is sealed in the module's table and refreshed as the official
// client does. Issuing follows the vendor's certificate step by step,
// waiting in between (contracts.Wait): it sends the CSR of the issuance's
// key, puts the files of the domain validation on the website, or its
// records in the DNS, through the certificate's solver, and downloads the
// certificate once issued.
//
// The API is not public; what JEC knows of it comes from reading the
// official client, which it does not run. The module is experimental, so
// it starts off.
package namecheap

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the namecheap module.
type Module struct {
	now func() time.Time
	// api talks to the vendor and web fetches websites as a CA does (see
	// httpx.Options.SkipVerify), both on the public internet only; tests
	// replace them.
	api, web *http.Client
	// visible tells whether the authoritative servers answer a record;
	// tests replace it.
	visible func(ctx context.Context, name, typ, value string) (bool, error)

	db        *sql.DB
	secrets   *secrets.Scope
	providers *providers.Scope
	log       *slog.Logger

	mu    sync.Mutex
	locks map[int64]*sync.Mutex // of the instances, by ID
}

// New returns the module.
func New() *Module {
	api, _ := httpx.Client(httpx.Options{Timeout: time.Minute, PublicOnly: true})
	web, _ := httpx.Client(httpx.Options{Timeout: 15 * time.Second, PublicOnly: true, SkipVerify: true})
	// The access token goes to the API only, not where a redirect points.
	api.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Module{now: time.Now, api: api, web: web, visible: dnscheck.Visible, locks: map[int64]*sync.Mutex{}}
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:           "namecheap",
		Category:     module.Source,
		Name:         i18n.NamecheapModuleName,
		Description:  i18n.NamecheapModuleDescription,
		Experimental: true,
	}
}

// Migrations returns the module's migrations.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Register adds the SSL Manager type and the job that keeps access tokens
// fresh.
func (m *Module) Register(r *module.Registry) error {
	m.db, m.secrets, m.providers, m.log = r.DB, r.Secrets, r.Providers, r.Log
	if err := r.Providers.Add(sslManagerType(), m.factory); err != nil {
		return err
	}
	return r.Jobs.Add(jobs.Job{Name: "refresh", Every: 6 * time.Hour, Timeout: 10 * time.Minute, Run: m.refreshAll})
}

// client returns the API of instance in.
func (m *Module) client(in providers.Instance) (*client, error) {
	base := strings.TrimSpace(in.Config["url"])
	if base == "" {
		base = defaultURL
	}
	if !strings.HasPrefix(base, "https://") { // the tokens go nowhere in the clear
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorUrl, Params: map[string]any{"url": base}})
	}
	return &client{base: base, http: m.api}, nil
}

// lock locks instance id, and returns the unlock.
func (m *Module) lock(id int64) func() {
	m.mu.Lock()
	l := m.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

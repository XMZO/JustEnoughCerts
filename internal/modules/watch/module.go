// Package watch is the module that watches the certificates of sites JEC
// does not manage, as a partner's or another team's: what each serves,
// whether it is trusted, and when it ends, with notes as the end comes
// (contracts.SiteExpiring).
package watch

import (
	"context"
	"crypto/x509"
	"database/sql"
	"embed"
	"io/fs"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the watch module.
type Module struct {
	db     *sql.DB
	events *events.Scope
	now    func() time.Time
	// fetch connects to address for host and returns the certificates it
	// serves, leaf first; tests replace it.
	fetch func(ctx context.Context, host, address string) ([]*x509.Certificate, error)
	// roots are the CAs a site's certificate has to chain to; nil for the
	// system's.
	roots *x509.CertPool
}

// New returns the module.
func New() *Module { return &Module{now: time.Now, fetch: fetch} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "watch",
		Category:    module.Feature,
		Name:        i18n.WatchModuleName,
		Description: i18n.WatchModuleDescription,
	}
}

// Migrations returns the table of the sites.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Register adds the API under /api/v1/watch, and the job that checks the
// sites as they come due.
func (m *Module) Register(r *module.Registry) error {
	m.db, m.events = r.DB, r.Events
	registerAPI(r.API, m)
	return r.Jobs.Add(jobs.Job{Name: "check", Every: 10 * time.Minute, Timeout: 10 * time.Minute, Run: m.checkDue})
}

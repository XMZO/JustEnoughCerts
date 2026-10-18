// Package monitor is the module of online checks (PLAN.md 3, Monitor). It
// connects to the domains of each certificate over TLS, as a browser does,
// and compares the certificate each serves with the version in use. A
// domain that still serves an older one a while after the version became
// current, as when a CDN did not take it, is told of once through
// contracts.CertificateServedOld, which notifiers pass on.
//
// The module knows the certificates from the events of the certs module;
// once a day, and soon after it first starts, it asks for all of them again
// with contracts.CertificatesAnnounce.
package monitor

import (
	"context"
	"crypto/x509"
	"database/sql"
	"embed"
	"io/fs"
	"log/slog"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the monitor module.
type Module struct {
	now func() time.Time
	// serve connects to a domain and returns the certificate it serves;
	// tests replace it.
	serve func(ctx context.Context, domain string) (*x509.Certificate, error)

	db     *sql.DB
	events *events.Scope
	log    *slog.Logger
}

// New returns the module.
func New() *Module { return &Module{now: time.Now, serve: served} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "monitor",
		Category:    module.Feature,
		Name:        i18n.MonitorModuleName,
		Description: i18n.MonitorModuleDescription,
	}
}

// Migrations returns the module's migrations.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Register adds the API under /api/v1/monitor, the subscription to the
// certificate events, and the jobs that check the domains due and ask for
// the certificates again.
func (m *Module) Register(r *module.Registry) error {
	m.db, m.events, m.log = r.DB, r.Events, r.Log
	registerAPI(r.API, m)
	err := r.Events.Subscribe(events.Subscription{
		Name: "certificates", Handler: m.handle,
		Types: []string{contracts.CertificateIssued, contracts.CertificateCurrent, contracts.CertificateDeployed, contracts.CertificateDeleted},
	})
	if err != nil {
		return err
	}
	for _, j := range []jobs.Job{
		{Name: "check", Every: time.Minute, Timeout: 10 * time.Minute, Run: m.checkDue},
		{Name: "announce", Every: 24 * time.Hour, Timeout: time.Minute, Run: m.announce},
	} {
		if err := r.Jobs.Add(j); err != nil {
			return err
		}
	}
	return nil
}

// announce asks the certs module for every certificate it has.
func (m *Module) announce(ctx context.Context) error {
	return m.events.Publish(ctx, contracts.CertificatesAnnounce, "", nil)
}

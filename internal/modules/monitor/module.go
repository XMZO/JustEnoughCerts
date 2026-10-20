// Package monitor is the module of online checks (PLAN.md 3, Monitor). It
// connects to the domains of each certificate over TLS, as a browser does,
// and compares the certificate each serves with the version in use. A
// domain that still serves an older one a while after the version became
// current, as when a CDN did not take it, is told of once through
// contracts.CertificateServedOld, which notifiers pass on; one that serves
// the version without the intermediates clients need, through
// contracts.CertificateChainIncomplete.
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
	"net/netip"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the monitor module.
type Module struct {
	now func() time.Time
	// serve connects to an address, or to a domain when "", for the domain
	// and returns the certificates it serves, leaf first; target says where
	// the DNS of a name sends the visitors of a subnet. Tests replace them.
	serve  func(ctx context.Context, domain, address string) ([]*x509.Certificate, error)
	target func(ctx context.Context, name string, subnet netip.Prefix) (string, error)
	// roots are those a chain served has to reach, nil for the system's;
	// issuers fetches the issuers a chain lacks, as its certificates name
	// them (AIA). Tests replace them.
	roots   *x509.CertPool
	issuers func(ctx context.Context, b *cryptox.Bundle) error

	db     *sql.DB
	events *events.Scope
	log    *slog.Logger
}

// New returns the module.
func New() *Module { return &Module{now: time.Now, serve: served, target: dnscheck.Target} }

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
	if m.issuers == nil {
		client, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: 15 * time.Second})
		if err != nil {
			return err
		}
		m.issuers = func(ctx context.Context, b *cryptox.Bundle) error { return cryptox.CompleteChain(ctx, client, b) }
	}
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

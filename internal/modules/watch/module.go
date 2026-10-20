// Package watch is the module that watches what ends outside JEC's hands:
// the certificates of sites JEC does not manage, as a partner's or another
// team's (what each serves, whether it is trusted, and when it ends), and
// the registrations of the domains of the certificates and the sites, as
// their registries tell through RDAP. Notes go out as an end comes
// (contracts.SiteExpiring, contracts.DomainExpiring). It can also search
// certificate transparency logs for certificates of those domains JEC does
// not hold (contracts.CertificateSpotted).
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
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the watch module.
type Module struct {
	db      *sql.DB
	events  *events.Scope
	secrets *secrets.Scope
	now     func() time.Time
	// fetch connects to address for host and returns the certificates it
	// serves, leaf first; tests replace it.
	fetch func(ctx context.Context, host, address string) ([]*x509.Certificate, error)
	// roots are the CAs a site's certificate has to chain to; nil for the
	// system's.
	roots *x509.CertPool
	// rdap looks up the registrations of domains; call asks the API, for
	// the domains of the certificates.
	rdap *rdap
	// spotter searches certificate transparency logs.
	spotter *spotter
	// issuers fetches the issuers a chain served lacks, as its
	// certificates name them (AIA); tests replace it.
	issuers func(ctx context.Context, b *cryptox.Bundle) error
	call    func(ctx context.Context, via, method, path string, body, out any) error
	// trigger makes a job due now.
	trigger func(name string)
}

// New returns the module.
func New() *Module {
	m := &Module{now: time.Now, fetch: fetch, trigger: func(string) {}}
	m.rdap = &rdap{bootstrap: bootstrapURL, now: func() time.Time { return m.now() }, whois: whoisQuery}
	m.spotter = &spotter{url: certSpotterURL}
	return m
}

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

// Register adds the API under /api/v1/watch, and the jobs that check the
// sites and look up the domains as they come due.
func (m *Module) Register(r *module.Registry) error {
	m.db, m.events, m.secrets, m.call, m.trigger = r.DB, r.Events, r.Secrets, r.Call, r.Jobs.Trigger
	if m.rdap.client == nil {
		client, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: 20 * time.Second})
		if err != nil {
			return err
		}
		m.rdap.client = client
	}
	if m.issuers == nil {
		client, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: 15 * time.Second})
		if err != nil {
			return err
		}
		m.issuers = func(ctx context.Context, b *cryptox.Bundle) error { return cryptox.CompleteChain(ctx, client, b) }
	}
	if m.spotter.client == nil {
		client, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: time.Minute})
		if err != nil {
			return err
		}
		m.spotter.client = client
	}
	registerAPI(r.API, m)
	for _, j := range []jobs.Job{
		{Name: "check", Every: 10 * time.Minute, Timeout: 10 * time.Minute, Run: m.checkDue},
		{Name: "domains", Every: 10 * time.Minute, Timeout: 10 * time.Minute, Run: m.domainsDue},
		{Name: "transparency", Every: 10 * time.Minute, Timeout: 10 * time.Minute, Run: m.transparencyDue},
	} {
		if err := r.Jobs.Add(j); err != nil {
			return err
		}
	}
	return nil
}

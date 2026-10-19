// Package certs is the certificates module: the certificates JEC keeps
// valid, each with the versions issued or uploaded for it (PLAN.md 3).
//
// A certificate is a set of domains with a source and a renewal policy; a
// version is one certificate with its chain and private key, which is
// sealed in the database. Versions are uploaded, or issued by a source
// provider such as an ACME account: each issuance is kept with its log,
// retried with a backoff when it fails, and started again at the check
// time of the settings once the current version enters its renewal window
// (PLAN.md 4.3 and 4.6). The
// module publishes the certificate events of package contracts, and warns
// once a version enters its renewal window, has a week left and expires.
package certs

import (
	"embed"
	"io/fs"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the certs module.
type Module struct {
	now func() time.Time
}

// New returns the module.
func New() *Module { return &Module{now: time.Now} }

// Manifest describes the module. It is required: everything else in JEC
// works on its certificates.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "certs",
		Category:    module.Feature,
		Name:        i18n.CertsModuleName,
		Description: i18n.CertsModuleDescription,
		Required:    true,
	}
}

// Migrations returns the module's migrations.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Register adds the API under /api/v1/certs, the jobs that issue, deploy,
// renew and check expiry, and the veto on deleting the providers
// certificates use.
func (m *Module) Register(r *module.Registry) error {
	s := newService(r.DB, r.Events, r.Secrets, r.Providers, r.Log, m.now)
	s.zone = r.Zone
	client, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	s.status = &statusChecker{client: client, now: m.now}
	s.issuers = client
	s.issuing.trigger = func() { r.Jobs.Trigger("issue") }
	s.deploying.trigger = func() { r.Jobs.Trigger("deploy") }
	registerAPI(r.API, s, r.ConfirmPassword)
	r.Providers.UsedBy(s.usesProvider)
	for _, sec := range s.configSections() {
		if err := r.Config(sec); err != nil {
			return err
		}
	}
	if err := r.Events.Subscribe(events.Subscription{Name: "announce", Types: []string{contracts.CertificatesAnnounce}, Handler: s.announce}); err != nil {
		return err
	}
	for _, j := range []jobs.Job{
		// Run until nothing is due; started at once when work is added.
		{Name: "issue", Every: time.Minute, Timeout: 24 * time.Hour, Run: s.runIssuances},
		{Name: "deploy", Every: time.Minute, Timeout: 24 * time.Hour, Run: s.runDeployments},
		// Often enough to start renewals near the check time.
		{Name: "renew", Every: 5 * time.Minute, Timeout: 5 * time.Minute, Run: s.renew},
		{Name: "check_expiry", Every: time.Hour, Timeout: 5 * time.Minute, Run: s.checkExpiry},
		{Name: "renewal_info", Every: time.Hour, Timeout: 10 * time.Minute, Run: s.checkRenewalInfo},
		{Name: "revocation", Every: 30 * time.Minute, Timeout: 10 * time.Minute, Run: s.checkRevocation},
		{Name: "digest", Every: time.Hour, Timeout: 5 * time.Minute, Run: s.digest},
	} {
		if err := r.Jobs.Add(j); err != nil {
			return err
		}
	}
	return nil
}

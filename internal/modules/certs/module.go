// Package certs is the certificates module: the certificates JEC keeps
// valid, each with the versions issued or uploaded for it (PLAN.md 3).
//
// A certificate is a set of domains with a source and a renewal policy; a
// version is one certificate with its chain and private key, which is
// sealed in the database. For now versions come from uploads. The module
// publishes the certificate events of package contracts, and warns once a
// version enters its renewal window, has a week left and expires.
package certs

import (
	"embed"
	"io/fs"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
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

// Register adds the API under /api/v1/certs and the expiry check.
func (m *Module) Register(r *module.Registry) error {
	s := &service{db: r.DB, events: r.Events, secrets: r.Secrets, log: r.Log, now: m.now}
	registerAPI(r.API, s, r.ConfirmPassword)
	return r.Jobs.Add(jobs.Job{Name: "check_expiry", Every: time.Hour, Timeout: 5 * time.Minute, Run: s.checkExpiry})
}

// Package lego is the module of lego's DNS providers (PLAN.md 5.5). Each
// DNS provider lego supports becomes a provider type of kind solver, which
// proves control of a domain for ACME by adding the TXT record of a DNS-01
// challenge.
//
// lego reads a provider's settings from environment variables, and some
// providers keep reading them after they start. So every provider runs in
// a child process (package children) whose environment holds its own
// settings and nothing else; the server talks to it over a pipe.
package lego

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the lego module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "lego",
		Category:    module.Solver,
		Name:        i18n.LegoModuleName,
		Description: i18n.LegoModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds a provider type for each DNS provider in the catalog.
func (*Module) Register(r *module.Registry) error {
	c, err := loadCatalog()
	if err != nil {
		return err
	}
	for _, p := range c.Providers {
		if err := r.Providers.Add(providerType(p), factory(p, r.Log)); err != nil {
			return err
		}
	}
	return nil
}

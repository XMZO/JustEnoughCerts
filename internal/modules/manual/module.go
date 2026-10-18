// Package manual is the module of DNS records added by hand: an issuance's
// log lists the TXT records of its DNS-01 challenges, people add them at
// their DNS provider, and the CA is told once they show. For domains whose
// DNS has no API JEC knows.
package manual

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the manual module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "manual",
		Category:    module.Solver,
		Name:        i18n.ManualModuleName,
		Description: i18n.ManualModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the solver type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(dnsType(), factory)
}

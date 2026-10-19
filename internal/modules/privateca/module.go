// Package privateca is the module of a private CA as a certificate source:
// JEC signs certificates for internal names with a CA of the user's own,
// whose certificate the devices that connect trust. There is nothing to
// validate.
package privateca

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the privateca module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "privateca",
		Category:    module.Source,
		Name:        i18n.PrivatecaModuleName,
		Description: i18n.PrivatecaModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the CA type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(caType(), factory)
}

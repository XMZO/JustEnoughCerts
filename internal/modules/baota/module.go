// Package baota is the module of BaoTa (aaPanel) as a deploy target:
// through the panel's API, each new certificate is set on websites of the
// panel, which then serves it.
package baota

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the baota module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "baota",
		Category:    module.Target,
		Name:        i18n.BaotaModuleName,
		Description: i18n.BaotaModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the site type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(siteType(), factory)
}

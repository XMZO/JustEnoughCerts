// Package gcore is the module of Gcore's CDN as a deploy target: each new
// certificate goes to Gcore's store of certificates, and then onto the CDN
// resources.
package gcore

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the gcore module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "gcore",
		Category:    module.Target,
		Name:        i18n.GcoreModuleName,
		Description: i18n.GcoreModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the CDN type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(cdnType(), factory)
}

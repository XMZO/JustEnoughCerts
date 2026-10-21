// Package kong is the module of Kong Gateway as a deploy target: through
// its Admin API, each new certificate goes into a certificate object of
// the gateway, with the certificate's domains as its SNIs.
package kong

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the kong module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "kong",
		Category:    module.Target,
		Name:        i18n.KongModuleName,
		Description: i18n.KongModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the certificate type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(certificateType(), factory)
}

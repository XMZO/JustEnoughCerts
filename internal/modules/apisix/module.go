// Package apisix is the module of Apache APISIX as a deploy target:
// through its Admin API, each new certificate goes into an SSL object of
// the gateway, which serves it for the certificate's domains.
package apisix

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the apisix module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "apisix",
		Category:    module.Target,
		Name:        i18n.ApisixModuleName,
		Description: i18n.ApisixModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the SSL object type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(sslType(), factory)
}

// Package npm is the module of Nginx Proxy Manager as a deploy target:
// through the panel's API, each new certificate goes into a custom
// certificate of the panel, which the hosts using it then serve.
package npm

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the npm module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "npm",
		Category:    module.Target,
		Name:        i18n.NpmModuleName,
		Description: i18n.NpmModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the certificate type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(certificateType(), factory)
}

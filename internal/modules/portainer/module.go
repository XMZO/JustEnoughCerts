// Package portainer is the module of Portainer as a deploy target: through
// its API, each new certificate becomes the one Portainer serves its own
// HTTPS with.
package portainer

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the portainer module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "portainer",
		Category:    module.Target,
		Name:        i18n.PortainerModuleName,
		Description: i18n.PortainerModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the panel type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(panelType(), factory)
}

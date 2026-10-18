// Package synology is the module of Synology DSM as a deploy target:
// through the Web API of the NAS, each new certificate replaces one of its
// certificates, which the services bound to it then serve.
package synology

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the synology module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "synology",
		Category:    module.Target,
		Name:        i18n.SynologyModuleName,
		Description: i18n.SynologyModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the DSM type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(dsmType(), factory)
}

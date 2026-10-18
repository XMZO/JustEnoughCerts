// Package bark is the module of Bark as a notifier: notes are pushed to an
// iPhone that has the Bark app, by its device key, through Bark's public
// server or one of your own.
package bark

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the bark module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "bark",
		Category:    module.Notifier,
		Name:        i18n.BarkModuleName,
		Description: i18n.BarkModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the device type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(deviceType(), factory)
}

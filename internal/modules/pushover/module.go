// Package pushover is the module of Pushover as a notifier: notes are
// pushed to the devices of a user or a group, through an application's
// token.
package pushover

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the pushover module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "pushover",
		Category:    module.Notifier,
		Name:        i18n.PushoverModuleName,
		Description: i18n.PushoverModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the user type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(userType(), factory)
}

// Package pushplus is the module of PushPlus as a notifier: notes go to
// WeChat, or to the other channels PushPlus delivers to, by a user's token.
package pushplus

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the pushplus module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "pushplus",
		Category:    module.Notifier,
		Name:        i18n.PushplusModuleName,
		Description: i18n.PushplusModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the token type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(tokenType(), factory)
}

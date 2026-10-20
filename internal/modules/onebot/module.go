// Package onebot is the module of QQ as a notifier, through a bot that
// speaks OneBot v11, such as NapCat or Lagrange: notes go to a person or a
// group.
package onebot

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the onebot module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "onebot",
		Category:    module.Notifier,
		Name:        i18n.OnebotModuleName,
		Description: i18n.OnebotModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the bot type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(botType(), factory)
}

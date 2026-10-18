// Package discord is the module of Discord as a notifier: notes go to a
// channel through a webhook of it.
package discord

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the discord module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "discord",
		Category:    module.Notifier,
		Name:        i18n.DiscordModuleName,
		Description: i18n.DiscordModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the webhook type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(webhookType(), factory)
}

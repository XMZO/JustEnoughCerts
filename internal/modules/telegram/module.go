// Package telegram is the module of Telegram bots as notifiers: notes go
// to a chat, a group or a channel through a bot made with @BotFather,
// optionally through a proxy, since Telegram is out of reach in some
// places.
package telegram

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the telegram module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "telegram",
		Category:    module.Notifier,
		Name:        i18n.TelegramModuleName,
		Description: i18n.TelegramModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the bot type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(botType(), factory)
}

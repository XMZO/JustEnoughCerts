// Package slack is the module of Slack as a notifier: notes go to a channel
// through an incoming webhook, a format Mattermost and Rocket.Chat take
// too.
package slack

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the slack module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "slack",
		Category:    module.Notifier,
		Name:        i18n.SlackModuleName,
		Description: i18n.SlackModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the webhook type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(webhookType(), factory)
}

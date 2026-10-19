// Package telegram is the module of Telegram bots: they send notes to a
// chat, a group or a channel through a bot made with @BotFather,
// optionally through a proxy, since Telegram is out of reach in some
// places; and, when their settings ask for it, they answer the commands
// of that chat with a menu of the certificates, which reads and acts
// through JEC's own API (listen.go, menu.go).
package telegram

import (
	"context"
	"io/fs"
	"log/slog"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// Module is the telegram module.
type Module struct {
	providers *providers.Scope
	call      func(ctx context.Context, via, method, path string, body, out any) error
	zone      func() *time.Location
	baseURL   string
	cat       *i18n.Catalog
	log       *slog.Logger
	// every is how often the bots' settings are looked at again, and
	// wait how long a poll of the Bot API waits for an update.
	every, wait time.Duration
	now         func() time.Time
}

// New returns the module.
func New() *Module { return &Module{every: 30 * time.Second, wait: 20 * time.Second, now: time.Now} }

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

// Register adds the bot type, and the worker that answers the commands of
// the bots that ask for it.
func (m *Module) Register(r *module.Registry) error {
	cat, err := i18n.Default()
	if err != nil {
		return err
	}
	m.providers, m.call, m.zone, m.baseURL, m.cat, m.log = r.Providers, r.Call, r.Zone, r.BaseURL, cat, r.Log
	if err := r.Providers.Add(botType(), factory); err != nil {
		return err
	}
	return r.Jobs.Work(jobs.Worker{Name: "commands", Run: m.serve})
}

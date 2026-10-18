// Package feishu is the module of Feishu (Lark) group bots as notifiers:
// notes go to a group through the webhook of a custom bot, signed when the
// bot asks for it.
package feishu

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// Module is the feishu module.
type Module struct {
	now func() time.Time
	// client reaches Feishu, and bases are the addresses of the webhooks
	// of its bots, and of Lark's; tests replace them.
	client *http.Client
	bases  []string
}

// New returns the module.
func New() *Module {
	client, _ := httpx.Client(httpx.Options{Timeout: 30 * time.Second, PublicOnly: true})
	return &Module{now: time.Now, client: client, bases: []string{feishuBase, larkBase}}
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "feishu",
		Category:    module.Notifier,
		Name:        i18n.FeishuModuleName,
		Description: i18n.FeishuModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the bot type.
func (m *Module) Register(r *module.Registry) error {
	return r.Providers.Add(botType(), m.factory)
}

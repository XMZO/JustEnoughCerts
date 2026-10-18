// Package wecom is the module of WeCom (WeChat Work) group robots as
// notifiers: notes go to a group through the webhook of a robot.
package wecom

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// Module is the wecom module.
type Module struct {
	// client reaches WeCom, and base is the address of its robots;
	// tests replace them.
	client *http.Client
	base   string
}

// New returns the module.
func New() *Module {
	client, _ := httpx.Client(httpx.Options{Timeout: 30 * time.Second, PublicOnly: true})
	return &Module{client: client, base: defaultBase}
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "wecom",
		Category:    module.Notifier,
		Name:        i18n.WecomModuleName,
		Description: i18n.WecomModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the robot type.
func (m *Module) Register(r *module.Registry) error {
	return r.Providers.Add(robotType(), m.factory)
}

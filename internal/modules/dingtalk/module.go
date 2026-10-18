// Package dingtalk is the module of DingTalk group robots as notifiers:
// notes go to a group through the webhook of a custom robot, signed when
// the robot asks for it.
package dingtalk

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// Module is the dingtalk module.
type Module struct {
	now func() time.Time
	// client reaches DingTalk, and base is the address of its robots;
	// tests replace them.
	client *http.Client
	base   string
}

// New returns the module.
func New() *Module {
	client, _ := httpx.Client(httpx.Options{Timeout: 30 * time.Second, PublicOnly: true})
	return &Module{now: time.Now, client: client, base: defaultBase}
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "dingtalk",
		Category:    module.Notifier,
		Name:        i18n.DingtalkModuleName,
		Description: i18n.DingtalkModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the robot type.
func (m *Module) Register(r *module.Registry) error {
	return r.Providers.Add(robotType(), m.factory)
}

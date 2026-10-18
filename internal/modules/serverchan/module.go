// Package serverchan is the module of ServerChan as a notifier: notes
// reach WeChat, and the other apps ServerChan pushes to, through a SendKey
// of ServerChan Turbo or of ServerChan3.
package serverchan

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// Module is the serverchan module.
type Module struct {
	// client reaches ServerChan; turbo is where Turbo's API is, and sc3
	// where ServerChan3's is for the uid of a key (%s). Tests replace them.
	client *http.Client
	turbo  string
	sc3    string
}

// New returns the module.
func New() *Module {
	client, _ := httpx.Client(httpx.Options{Timeout: 30 * time.Second, PublicOnly: true})
	return &Module{client: client, turbo: turboBase, sc3: sc3Base}
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "serverchan",
		Category:    module.Notifier,
		Name:        i18n.ServerchanModuleName,
		Description: i18n.ServerchanModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the SendKey type.
func (m *Module) Register(r *module.Registry) error {
	return r.Providers.Add(sendKeyType(), m.factory)
}

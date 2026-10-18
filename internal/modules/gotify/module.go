// Package gotify is the module of Gotify as a notifier: notes are pushed
// as an application of your Gotify server, and reach its apps and web UI.
package gotify

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the gotify module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "gotify",
		Category:    module.Notifier,
		Name:        i18n.GotifyModuleName,
		Description: i18n.GotifyModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the application type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(appType(), factory)
}

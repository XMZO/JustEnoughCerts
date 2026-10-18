// Package ntfy is the module of ntfy as a notifier: notes are published to
// a topic, on ntfy.sh or a server of your own, and reach every phone and
// browser subscribed to it.
package ntfy

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the ntfy module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "ntfy",
		Category:    module.Notifier,
		Name:        i18n.NtfyModuleName,
		Description: i18n.NtfyModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the topic type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(topicType(), factory)
}

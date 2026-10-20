// Package teams is the module of Microsoft Teams as a notifier: notes go to
// a channel or chat through a webhook of the Workflows app.
package teams

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the teams module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "teams",
		Category:    module.Notifier,
		Name:        i18n.TeamsModuleName,
		Description: i18n.TeamsModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the workflow type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(workflowType(), factory)
}

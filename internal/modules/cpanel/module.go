// Package cpanel is the module of cPanel as a deploy target: through
// UAPI, with an account's API token, each new certificate is installed on
// websites of the account, as shared hosting often runs it.
package cpanel

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the cpanel module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "cpanel",
		Category:    module.Target,
		Name:        i18n.CpanelModuleName,
		Description: i18n.CpanelModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the account type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(accountType(), factory)
}

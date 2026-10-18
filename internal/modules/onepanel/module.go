// Package onepanel is the module of 1Panel as a deploy target: through the
// panel's API, each new certificate replaces the content of a certificate
// of the panel, which the websites using it then serve.
package onepanel

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the onepanel module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "onepanel",
		Category:    module.Target,
		Name:        i18n.OnepanelModuleName,
		Description: i18n.OnepanelModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the certificate type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(certificateType(), factory)
}

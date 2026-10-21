// Package safeline is the module of SafeLine, Chaitin's web application
// firewall, as a deploy target: through its open API, each new
// certificate replaces the content of a certificate of the firewall, which
// the sites using it then serve.
package safeline

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the safeline module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "safeline",
		Category:    module.Target,
		Name:        i18n.SafelineModuleName,
		Description: i18n.SafelineModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the certificate type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(certificateType(), factory)
}

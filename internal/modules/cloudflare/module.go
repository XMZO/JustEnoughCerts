// Package cloudflare is the module of Cloudflare's custom certificates as
// a deploy target: a zone serves the certificate JEC uploads in place of
// Cloudflare's own, for the hosts it covers. Custom certificates need a
// Business or Enterprise plan.
package cloudflare

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the cloudflare module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "cloudflare",
		Category:    module.Target,
		Name:        i18n.CloudflareModuleName,
		Description: i18n.CloudflareModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the custom certificate type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(customType(), factory)
}

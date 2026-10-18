// Package upyun is the module of UPYUN's CDN as a deploy target: each new
// certificate goes to UPYUN's store of certificates, and then onto the
// accelerated domains. UPYUN's open API only reads certificates, so the
// module goes through the console's own interface, signing in with the
// account's name and password, as other tools do: it is experimental, as
// that interface is not a published one and may change.
package upyun

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the upyun module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:           "upyun",
		Category:     module.Target,
		Name:         i18n.UpyunModuleName,
		Description:  i18n.UpyunModuleDescription,
		Experimental: true,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the CDN type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(cdnType(), factory)
}

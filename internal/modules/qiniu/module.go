// Package qiniu is the module of Qiniu Cloud's CDN as a deploy target:
// each new certificate goes to Qiniu's store of certificates, and then
// onto the accelerated domains.
package qiniu

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the qiniu module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "qiniu",
		Category:    module.Target,
		Name:        i18n.QiniuModuleName,
		Description: i18n.QiniuModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the CDN type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(cdnType(), factory)
}

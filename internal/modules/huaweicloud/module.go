// Package huaweicloud is the module of Huawei Cloud's CDN as a deploy
// target: each new certificate is set on the accelerated domains, with
// HTTPS on, and the domains keep their other HTTPS settings, such as
// HTTP/2 and the TLS versions.
package huaweicloud

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the huaweicloud module.
type Module struct {
	// endpoint replaces the API's address, as tests do; "" for the site's.
	endpoint string
}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "huaweicloud",
		Category:    module.Target,
		Name:        i18n.HuaweicloudModuleName,
		Description: i18n.HuaweicloudModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the CDN type.
func (m *Module) Register(r *module.Registry) error {
	return r.Providers.Add(cdnType(), m.factory)
}

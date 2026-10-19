// Package huaweicloud is the module of Huawei Cloud's CDN and ELB as deploy
// targets. On the CDN each new certificate is set on the accelerated
// domains, with HTTPS on, and the domains keep their other HTTPS settings,
// such as HTTP/2 and the TLS versions. On ELB, JEC keeps a certificate
// that it updates in place, and the listeners set serve it.
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

// Register adds the CDN and ELB types.
func (m *Module) Register(r *module.Registry) error {
	if err := r.Providers.Add(cdnType(), m.factory); err != nil {
		return err
	}
	return r.Providers.Add(elbType(), m.elbFactory)
}

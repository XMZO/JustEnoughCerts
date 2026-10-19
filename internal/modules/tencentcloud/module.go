// Package tencentcloud is the module of Tencent Cloud as a deploy target,
// through its SSL Certificates service: each new certificate is uploaded
// there, and replaces the one it follows on every resource that one is
// deployed to, CDN, EdgeOne, CLB and the others the service updates in one
// go; or goes straight to the domains of CDN or the hosts of EdgeOne. The certificates the service issues are a source too, and so are
// the free ones JEC applies for there.
package tencentcloud

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the tencentcloud module.
type Module struct {
	// open makes the API at endpoint; tests replace it.
	open func(id, key, endpoint string) (sslAPI, error)
}

// New returns the module.
func New() *Module { return &Module{open: newSDK} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "tencentcloud",
		Category:    module.Target,
		Name:        i18n.TencentcloudModuleName,
		Description: i18n.TencentcloudModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the SSL type, and the sources of the certificates SSL
// Certificates issued and of the free ones JEC applies for there.
func (m *Module) Register(r *module.Registry) error {
	if err := r.Providers.Add(sslType(), m.factory); err != nil {
		return err
	}
	if err := r.Providers.Add(cdnType(), m.directFactory("cdn")); err != nil {
		return err
	}
	if err := r.Providers.Add(teoType(), m.directFactory("teo")); err != nil {
		return err
	}
	if err := r.Providers.Add(fetchType(), m.fetchFactory); err != nil {
		return err
	}
	return r.Providers.Add(freeType(), m.freeFactory)
}

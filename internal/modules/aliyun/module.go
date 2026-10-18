// Package aliyun is the module of Alibaba Cloud's CDN and DCDN (Dynamic
// Route for CDN) as deploy targets: each new certificate is set on the
// accelerated domains, as acme.sh's ali_cdn and ali_dcdn hooks do. ESA has
// a module of its own.
package aliyun

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the aliyun module.
type Module struct {
	// open makes the API of a service; tests replace it.
	open func(svc service, id, secret string) (cdnAPI, error)
}

// New returns the module.
func New() *Module { return &Module{open: newSDK} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "aliyun",
		Category:    module.Target,
		Name:        i18n.AliyunModuleName,
		Description: i18n.AliyunModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the CDN and DCDN types.
func (m *Module) Register(r *module.Registry) error {
	for _, svc := range []service{cdn, dcdn} {
		if err := r.Providers.Add(svc.providerType(), m.factory(svc)); err != nil {
			return err
		}
	}
	return nil
}

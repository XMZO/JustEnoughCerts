// Package aliyun is the module of Alibaba Cloud's products as deploy
// targets: CDN and DCDN (Dynamic Route for CDN), where each new
// certificate is set on the accelerated domains, as acme.sh's ali_cdn and
// ali_dcdn hooks do; the Certificate Management Service, where it is
// uploaded for the other products; and CLB, the Classic Load Balancer. ESA
// has a module of its own.
package aliyun

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the aliyun module.
type Module struct {
	// open makes the API of CDN or DCDN, and rpc that of another service
	// at an endpoint; tests replace them.
	open func(svc service, id, secret string) (cdnAPI, error)
	rpc  func(endpoint, version, id, secret string) (rpc, error)
}

// New returns the module.
func New() *Module { return &Module{open: newSDK, rpc: newRPC} }

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

// Register adds the CDN, DCDN, Certificate Management Service and CLB
// types.
func (m *Module) Register(r *module.Registry) error {
	for _, svc := range []service{cdn, dcdn} {
		if err := r.Providers.Add(svc.providerType(), m.factory(svc)); err != nil {
			return err
		}
	}
	if err := r.Providers.Add(casType(), m.casFactory); err != nil {
		return err
	}
	return r.Providers.Add(clbType(), m.clbFactory)
}

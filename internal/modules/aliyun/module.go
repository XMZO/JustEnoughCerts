// Package aliyun is the module of Alibaba Cloud's products as deploy
// targets: CDN and DCDN (Dynamic Route for CDN), where each new
// certificate is set on the accelerated domains, as acme.sh's ali_cdn and
// ali_dcdn hooks do; the Certificate Management Service, where it is
// uploaded for the other products; the custom domains of OSS buckets; and
// CLB, the Classic Load Balancer. ESA has a module of its own. The
// Certificate Management Service is a source too: of the certificates it
// issued, and of those JEC applies for there with keys of its own.
package aliyun

import (
	"context"
	"io/fs"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// Module is the aliyun module.
type Module struct {
	// open makes the API of CDN or DCDN, and rpc that of another service
	// at an endpoint; tests replace them.
	open func(svc service, id, secret string) (cdnAPI, error)
	rpc  func(endpoint, version, id, secret string) (rpc, error)
	// web fetches validation files as a CA does, on the public internet
	// only and following no redirects, and visible tells whether the
	// authoritative servers answer a record; tests replace them.
	web     *http.Client
	visible func(ctx context.Context, name, typ, value string) (bool, error)
}

// New returns the module.
func New() *Module {
	web, _ := httpx.Client(httpx.Options{Timeout: 15 * time.Second, PublicOnly: true})
	web.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Module{open: newSDK, rpc: newRPC, web: web, visible: dnscheck.Visible}
}

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

// Register adds the CDN, DCDN, Certificate Management Service, OSS and
// CLB types, and the sources of the certificates the Certificate
// Management Service issued and of those JEC applies for there.
func (m *Module) Register(r *module.Registry) error {
	for _, svc := range []service{cdn, dcdn} {
		if err := r.Providers.Add(svc.providerType(), m.factory(svc)); err != nil {
			return err
		}
	}
	if err := r.Providers.Add(casType(), m.casFactory); err != nil {
		return err
	}
	if err := r.Providers.Add(ossType(), m.ossFactory); err != nil {
		return err
	}
	if err := r.Providers.Add(clbType(), m.clbFactory); err != nil {
		return err
	}
	if err := r.Providers.Add(fetchType(), m.fetchFactory); err != nil {
		return err
	}
	return r.Providers.Add(applyType(), m.applyFactory)
}

// Package baiducloud is the module of Baidu AI Cloud's CDN as a deploy
// target: each new certificate is set on the accelerated domains, with
// HTTPS on (PUT /v2/{domain}/certificates), as acme.sh's and certimate's
// deployments do. The requests are signed as bce-auth-v1 asks, by JEC
// itself: the SDK lego brings along logs the signatures it makes.
package baiducloud

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// Module is the baiducloud module.
type Module struct {
	// client and base reach the API, on the public internet only; tests
	// replace them.
	client *http.Client
	base   string
}

// New returns the module.
func New() *Module {
	client, _ := httpx.Client(httpx.Options{Timeout: time.Minute, PublicOnly: true})
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Module{client: client, base: "https://" + cdnHost}
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "baiducloud",
		Category:    module.Target,
		Name:        i18n.BaiducloudModuleName,
		Description: i18n.BaiducloudModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the CDN type.
func (m *Module) Register(r *module.Registry) error {
	return r.Providers.Add(cdnType(), m.cdnFactory)
}

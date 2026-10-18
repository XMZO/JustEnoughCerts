// Package volcengine is the module of Volcengine's CDN as a deploy target:
// each new certificate is uploaded to the certificate center, unless it
// is there from an earlier attempt, and deployed to the accelerated
// domains (BatchDeployCert), as certimate does. The requests are signed by
// the SDK lego brings along, which JEC uses for that alone.
package volcengine

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// Module is the volcengine module.
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
		ID:          "volcengine",
		Category:    module.Target,
		Name:        i18n.VolcengineModuleName,
		Description: i18n.VolcengineModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the CDN type.
func (m *Module) Register(r *module.Registry) error {
	return r.Providers.Add(cdnType(), m.cdnFactory)
}

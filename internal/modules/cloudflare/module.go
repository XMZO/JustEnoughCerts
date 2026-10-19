// Package cloudflare is the module of Cloudflare: its custom certificates
// as a deploy target, where a zone serves the certificate JEC uploads in
// place of Cloudflare's own, for the hosts it covers (custom certificates
// need a Business or Enterprise plan); and HTTP validations answered at
// its edge, by a Worker of JEC's, for websites behind it.
package cloudflare

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// Module is the cloudflare module.
type Module struct {
	// base is the address of the API; check fetches validation files as a
	// CA does; poll and propagate pace the wait for the edge to answer.
	base            string
	check           *http.Client
	poll, propagate time.Duration
}

// New returns the module.
func New() *Module {
	check, _ := httpx.Client(httpx.Options{Timeout: 15 * time.Second, PublicOnly: true, SkipVerify: true})
	return &Module{base: api, check: check, poll: 5 * time.Second, propagate: 3 * time.Minute}
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "cloudflare",
		Category:    module.Target,
		Name:        i18n.CloudflareModuleName,
		Description: i18n.CloudflareModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the custom certificate type and the answers at the edge.
func (m *Module) Register(r *module.Registry) error {
	if err := r.Providers.Add(customType(), factory); err != nil {
		return err
	}
	return r.Providers.Add(edgeType(), m.edgeFactory)
}

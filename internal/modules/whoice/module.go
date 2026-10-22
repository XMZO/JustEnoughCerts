// Package whoice is the module that looks up when a domain's registration
// ends where the expiry watch of module watch finds nothing: more
// registries' RDAP and WHOIS servers and the forms of query they want, the
// parsers of some 50 registries' answers, the web lookups of registries
// with neither (.lk, .ni, .pa and others), and the delegation of a name in
// its parent zone where no lookup answers (.al, .uy), all from Whoice
// (github.com/XMZO/whoice), in package lookup. Module watch asks it through
// the API (GET /api/v1/whoice/domains/{name}) and does without it when it
// is off.
package whoice

import (
	"io/fs"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/lookup"
)

// Module is the whoice module.
type Module struct {
	lookup *lookup.Lookup
}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "whoice",
		Category:    module.Feature,
		Name:        i18n.WhoiceModuleName,
		Description: i18n.WhoiceModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the API under /api/v1/whoice.
func (m *Module) Register(r *module.Registry) error {
	if m.lookup == nil {
		client, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: 20 * time.Second})
		if err != nil {
			return err
		}
		fetch, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: 10 * time.Second})
		if err != nil {
			return err
		}
		m.lookup = lookup.New(client, fetch)
	}
	registerAPI(r.API, m)
	return nil
}

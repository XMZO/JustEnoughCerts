// Package esa is the module of Alibaba Cloud ESA (Edge Security
// Acceleration) as a deploy target (PLAN.md 5.4). A target is a site of an
// account: each new version of a certificate is uploaded to it and,
// because the free plan holds two custom certificates per site, the
// versions uploaded before are removed once the new one is there.
package esa

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// Module is the esa module.
type Module struct {
	// newAPI makes the client of an instance; tests replace it.
	newAPI func(in providers.Instance) (siteAPI, error)
}

// New returns the module.
func New() *Module { return &Module{newAPI: newSDK} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "esa",
		Category:    module.Target,
		Name:        i18n.EsaModuleName,
		Description: i18n.EsaModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the site type.
func (m *Module) Register(r *module.Registry) error {
	return r.Providers.Add(siteType(), m.factory)
}

// Package caddy is the module of Caddy as a deploy target, through its
// admin API: each certificate of JEC is one entry of the certificates
// Caddy loads from PEM (apps.tls.certificates.load_pem), named by its @id,
// which each new version replaces in place. Caddy serves a certificate so
// loaded in place of one it manages itself, and applies a change at once,
// keeping its config as it was when the change fails.
//
// What the API changes lives in Caddy's memory, and in its autosave.json:
// a Caddy that starts from a Caddyfile again, without --resume, loses
// it, and JEC's online checks tell.
package caddy

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the caddy module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "caddy",
		Category:    module.Target,
		Name:        i18n.CaddyModuleName,
		Description: i18n.CaddyModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the admin API type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(adminType(), factory)
}

// Package proxmox is the module of Proxmox VE as a deploy target: through
// its API, each new certificate becomes the custom certificate of a node,
// which its web interface and API serve.
package proxmox

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the proxmox module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "proxmox",
		Category:    module.Target,
		Name:        i18n.ProxmoxModuleName,
		Description: i18n.ProxmoxModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the node type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(nodeType(), factory)
}

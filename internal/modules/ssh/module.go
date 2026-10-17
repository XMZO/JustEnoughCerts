// Package ssh is the module of servers reached over SSH: as deploy
// targets, each new version of a certificate is written to a directory
// there, as certbot lays it out, and a command such as reloading nginx
// runs afterwards; as solvers, the files of validations over HTTP go to
// the web root of the domains there. The server's host key is always
// checked.
package ssh

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the ssh module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "ssh",
		Category:    module.Target,
		Name:        i18n.SshModuleName,
		Description: i18n.SshModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the server and web root types.
func (*Module) Register(r *module.Registry) error {
	if err := r.Providers.Add(serverType(), factory); err != nil {
		return err
	}
	return r.Providers.Add(webrootType(), webrootFactory)
}

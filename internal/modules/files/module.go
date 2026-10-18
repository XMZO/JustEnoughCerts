// Package files is the module of local files: as a deploy target, each new
// version of a certificate is written to a directory, laid out as certbot
// does (cert.pem, chain.pem, fullchain.pem and privkey.pem), for a web
// server on the same machine or a mounted volume to read; as a solver, the
// files of validations over HTTP go to the web root of the domains; as a
// source, the certificates another ACME client renews in a directory come
// in with their keys.
package files

import (
	"errors"
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the files module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "files",
		Category:    module.Target,
		Name:        i18n.FilesModuleName,
		Description: i18n.FilesModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the directory, web root and watched directory types.
func (*Module) Register(r *module.Registry) error {
	return errors.Join(
		r.Providers.Add(directoryType(), factory),
		r.Providers.Add(webrootType(), webrootFactory),
		r.Providers.Add(watchType(), watchFactory),
	)
}

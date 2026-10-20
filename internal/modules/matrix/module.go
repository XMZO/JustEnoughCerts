// Package matrix is the module of Matrix as a notifier: notes go to a room,
// sent by an account of a homeserver.
package matrix

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the matrix module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "matrix",
		Category:    module.Notifier,
		Name:        i18n.MatrixModuleName,
		Description: i18n.MatrixModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the room type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(roomType(), factory)
}

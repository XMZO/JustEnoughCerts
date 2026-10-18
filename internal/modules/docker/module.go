// Package docker is the module of Docker containers as deployment targets:
// through the Docker Engine API, the files of a certificate go into a
// container, which is then signaled to reload them, or restarted, with no
// docker command needed.
package docker

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the docker module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "docker",
		Category:    module.Target,
		Name:        i18n.DockerModuleName,
		Description: i18n.DockerModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the container type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(containerType(), factory)
}

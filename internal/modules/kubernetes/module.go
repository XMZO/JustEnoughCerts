// Package kubernetes is the module of Kubernetes as a deploy target: each
// new certificate goes into a TLS Secret, through the API server, for
// Ingresses and other workloads to use.
package kubernetes

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the kubernetes module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "kubernetes",
		Category:    module.Target,
		Name:        i18n.KubernetesModuleName,
		Description: i18n.KubernetesModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the Secret type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(secretType(), factory)
}

// Package aws is the module of Amazon Web Services as a deploy target: each
// new certificate goes into AWS Certificate Manager, imported again over
// the same certificate, which CloudFront, load balancers and the other
// services using it then serve.
package aws

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the aws module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "aws",
		Category:    module.Target,
		Name:        i18n.AwsModuleName,
		Description: i18n.AwsModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the ACM and CloudFront types.
func (*Module) Register(r *module.Registry) error {
	if err := r.Providers.Add(acmType(), factory); err != nil {
		return err
	}
	return r.Providers.Add(cloudFrontType(), cloudFrontFactory)
}

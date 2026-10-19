// Package metrics is the module of JEC's metrics for Prometheus: when each
// certificate ends and renews, what state it is in, its failed deployments
// and what the online checks found, read from JEC's own API.
package metrics

import (
	"context"
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the metrics module.
type Module struct {
	call func(ctx context.Context, via, method, path string, body, out any) error
}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "metrics",
		Category:    module.Feature,
		Name:        i18n.MetricsModuleName,
		Description: i18n.MetricsModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds GET /api/v1/metrics/prometheus.
func (m *Module) Register(r *module.Registry) error {
	m.call = r.Call
	register(r.API, m)
	return nil
}

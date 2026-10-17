// Package webhook is the module of webhooks: HTTP POST requests with JSON
// bodies to an endpoint someone runs. As a notifier it posts each note; as
// a deploy target it posts each new version of a certificate with its
// private key, for the receiver to install, and so only over HTTPS. Both
// sign their requests when given a secret, and reach only public
// addresses unless set to allow private ones (PLAN.md 10).
package webhook

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the webhook module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "webhook",
		Category:    module.Notifier,
		Name:        i18n.WebhookModuleName,
		Description: i18n.WebhookModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the notifier and the deploy target.
func (*Module) Register(r *module.Registry) error {
	if err := r.Providers.Add(notifyType(), newNotifier); err != nil {
		return err
	}
	return r.Providers.Add(deployType(), newDeployer)
}

// Package email is the module of mail as a notifier: notes go out through
// an SMTP server, such as a mailbox provider's, with implicit TLS,
// STARTTLS, or no encryption for a relay on a trusted network.
package email

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the email module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "email",
		Category:    module.Notifier,
		Name:        i18n.EmailModuleName,
		Description: i18n.EmailModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the SMTP type.
func (*Module) Register(r *module.Registry) error {
	return r.Providers.Add(smtpType(), factory)
}

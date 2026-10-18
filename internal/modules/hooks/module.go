// Package hooks is the module of hooks (PLAN.md 5.2): scripts of your own
// that run when something happens to a certificate, to reach a system no
// other notifier does. A hook is a notifier (contracts.Notifier), so the
// notify module runs it for the events it is set to, as it sends notes,
// and tests it the same way. The script runs with the shell built into
// JEC, in the directory hooks in JEC's data directory, with the note in
// its environment and, as JSON, on its standard input.
//
// Hooks that need the certificate's files are deploy targets instead: the
// local directory one runs a command once it wrote them, as certbot's
// deploy hooks do, and the SSH one does so on a server.
package hooks

import (
	"context"
	"io/fs"
	"path/filepath"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// Module is the hooks module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "hooks",
		Category:    module.Notifier,
		Name:        i18n.HooksModuleName,
		Description: i18n.HooksModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the script type.
func (*Module) Register(r *module.Registry) error {
	dir := filepath.Join(r.DataDir, "hooks")
	return r.Providers.Add(scriptType(), func(_ context.Context, in providers.Instance) (any, error) {
		s, err := newScript(in, dir)
		if err != nil {
			return nil, err
		}
		return s, nil
	})
}

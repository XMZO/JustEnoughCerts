// Package plugins is the module of external plugins (PLAN.md 5.2): the
// executables in the plugins directory of JEC's data, written in any
// language, describe the provider types they offer, with their own
// translations, and JEC then runs them as deploy targets, notifiers and
// solvers. Each action runs the plugin once, with a JSON request on its
// standard input and its JSON answer on its standard output;
// docs/development/plugins.md is the protocol.
package plugins

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the plugins module.
type Module struct {
	dir string
	// tags are the languages JEC speaks.
	tags []language.Tag
	// found is what the plugins directory held when JEC started.
	found []*loaded
}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "plugins",
		Category:    module.Feature,
		Name:        i18n.PluginsModuleName,
		Description: i18n.PluginsModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing in the database.
func (*Module) Migrations() fs.FS { return nil }

// Register runs each plugin of the plugins directory to have it describe
// itself, and registers the types it offers. The types are fixed once JEC
// runs: it sees new and changed plugins when it starts again.
func (m *Module) Register(r *module.Registry) error {
	cat, err := i18n.Default()
	if err != nil {
		return err
	}
	m.dir, m.tags = filepath.Join(r.DataDir, "plugins"), cat.Tags()
	found, err := scan(m.dir)
	if err != nil {
		r.Log.Warn("cannot read the plugins directory", "dir", m.dir, "err", err)
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, l := range found {
		if l.err != nil {
			continue
		}
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			if stderr := l.describe(context.Background(), m.tags); l.err != nil && len(stderr) > 0 {
				r.Log.Warn("plugin standard error", "file", l.file, "stderr", string(stderr))
			}
		})
	}
	wg.Wait()
	for _, l := range found {
		for _, t := range l.types {
			if l.err != nil {
				break
			}
			spec := l.specs[strings.TrimPrefix(t.ID, "plugins."+l.plugin.id+".")]
			if err := r.Providers.Add(t, factory(l.plugin, spec)); err != nil {
				l.fail(i18n.PluginsErrorDescribe, map[string]any{"plugin": l.plugin.id, "problem": err.Error()})
				break
			}
			l.added = append(l.added, t.ID)
		}
		if l.err != nil {
			r.Log.Warn("plugin not in use", "file", l.file, "problem", l.err.Key, "params", l.err.Params)
			continue
		}
		r.Log.Info("plugin loaded", "file", l.file, "types", l.added)
	}
	m.found = found
	registerAPI(r.API, m)
	return nil
}

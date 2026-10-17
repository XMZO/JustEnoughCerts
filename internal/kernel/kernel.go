// Package kernel assembles the server: the store, the modules, the HTTP API
// in front of them, and the event bus and scheduler behind them.
package kernel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/web"
)

// owner names the kernel where it stands beside the modules: in migration
// tables, events and jobs.
const owner = "kernel"

// App is the assembled server.
type App struct {
	store   *store.Store
	modules *module.Set
	api     *api.API
	handler http.Handler
	bus     *events.Bus
	jobs    *jobs.Scheduler

	stop    context.CancelFunc
	running sync.WaitGroup
}

// New opens and migrates the store, loads which modules are on and lets
// every module register its parts.
func New(ctx context.Context, cfg config.Config, log *slog.Logger, mods []module.Module) (app *App, err error) {
	set, err := module.NewSet(mods...)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, st.Close())
		}
	}()

	if err := migrate(ctx, st, log, owner, migrations.FS); err != nil {
		return nil, err
	}
	for _, m := range set.Modules() {
		if err := migrate(ctx, st, log, m.Manifest().ID, m.Migrations()); err != nil {
			return nil, err
		}
	}
	if err := set.Load(ctx, st.DB()); err != nil {
		return nil, err
	}
	box, err := openSecrets(ctx, st.DB(), cfg, log)
	if err != nil {
		return nil, err
	}
	enabled := func(id string) bool { return id == owner || set.Enabled(id) }
	bus := events.New(st.DB(), log, enabled)
	sched := jobs.New(st.DB(), log, enabled)

	cookiePath := cfg.BasePath()
	if cookiePath == "" {
		cookiePath = "/"
	}
	accounts, err := auth.New(ctx, st.DB(), log, auth.Options{
		CookiePath: cookiePath,
		Secure:     strings.HasPrefix(cfg.BaseURL, "https://"),
	})
	if err != nil {
		return nil, err
	}
	a, err := api.New(api.Options{
		BasePath:      cfg.BasePath(),
		Origin:        origin(cfg.BaseURL),
		Version:       buildinfo.Get().Version,
		Middlewares:   []func(huma.Context, func(huma.Context)){accounts.Middleware},
		SessionCookie: auth.CookieName,
	}, set, log)
	if err != nil {
		return nil, err
	}
	accounts.Register(a.Kernel())
	for _, m := range set.Modules() {
		id := m.Manifest().ID
		r := &module.Registry{
			API:     a.Module(id, func() bool { return set.Enabled(id) }),
			DB:      st.DB(),
			Log:     log.With("module", id),
			Events:  bus.Scope(id),
			Jobs:    sched.Scope(id),
			Secrets: box.Scope(id),
		}
		if err := m.Register(r); err != nil {
			return nil, fmt.Errorf("module %s: %w", id, err)
		}
	}
	if err := sched.Scope(owner).Add(jobs.Job{Name: "prune_events", Every: 24 * time.Hour, Run: bus.Prune}); err != nil {
		return nil, err
	}
	bus.Seal()

	router := chi.NewRouter()
	router.Mount(cfg.BasePath()+api.Prefix, a.Handler())
	ui, err := web.Handler(cfg.BasePath())
	switch {
	case errors.Is(err, web.ErrNotBuilt):
		log.Warn(err.Error())
	case err != nil:
		return nil, err
	default:
		router.Handle(cfg.BasePath()+"/*", ui)
		if cfg.BasePath() != "" {
			router.Handle(cfg.BasePath(), ui)
		}
	}
	return &App{store: st, modules: set, api: a, handler: router, bus: bus, jobs: sched}, nil
}

// Start runs the event bus and the scheduler in the background until Close.
func (a *App) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	a.stop = cancel
	a.running.Add(2)
	go func() {
		defer a.running.Done()
		a.bus.Run(ctx)
	}()
	go func() {
		defer a.running.Done()
		a.jobs.Run(ctx)
	}()
}

// openSecrets loads the master key and makes sure it is the one the
// database was sealed with, so that a changed key fails at startup rather
// than when a secret is needed.
func openSecrets(ctx context.Context, db *sql.DB, cfg config.Config, log *slog.Logger) (*secrets.Box, error) {
	box, created, err := secrets.Load(cfg.DataDir, cfg.SecretKey)
	if err != nil {
		return nil, err
	}
	if created {
		log.Info("created the secret key file: back it up with the database", "path", filepath.Join(cfg.DataDir, secrets.KeyFile))
	}
	var check []byte
	err = db.QueryRowContext(ctx, "SELECT value FROM kernel_meta WHERE name = 'secret_check'").Scan(&check)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = db.ExecContext(ctx, "INSERT INTO kernel_meta (name, value) VALUES ('secret_check', ?)", box.Check())
		return box, err
	case err != nil:
		return nil, err
	case !box.Verify(check):
		return nil, fmt.Errorf("the secret key is not the one this database was sealed with: restore %s, or the JEC_SECRET_KEY used before",
			filepath.Join(cfg.DataDir, secrets.KeyFile))
	}
	return box, nil
}

// OpenAPI returns the document describing the API, every module included.
func (a *App) OpenAPI() *huma.OpenAPI { return a.api.Kernel().OpenAPI() }

// origin returns scheme://host[:port] of the base URL, or "".
func origin(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func migrate(ctx context.Context, st *store.Store, log *slog.Logger, owner string, fsys fs.FS) error {
	applied, err := st.Migrate(ctx, owner, fsys)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		log.Info("database migrated", "owner", owner, "versions", applied)
	}
	return nil
}

// Handler serves everything but the health check.
func (a *App) Handler() http.Handler { return a.handler }

// Modules returns the module set.
func (a *App) Modules() *module.Set { return a.modules }

// Close stops what Start started, waiting for the event handlers and jobs
// that are running, and releases the store.
func (a *App) Close() error {
	if a.stop != nil {
		a.stop()
		a.running.Wait()
	}
	return a.store.Close()
}

// Package kernel assembles the server: the store, the modules and the HTTP
// API in front of them.
package kernel

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/web"
)

// App is the assembled server.
type App struct {
	store   *store.Store
	modules *module.Set
	api     *api.API
	handler http.Handler
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

	if err := migrate(ctx, st, log, "kernel", migrations.FS); err != nil {
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
			API: a.Module(id, func() bool { return set.Enabled(id) }),
			DB:  st.DB(),
			Log: log.With("module", id),
		}
		if err := m.Register(r); err != nil {
			return nil, fmt.Errorf("module %s: %w", id, err)
		}
	}

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
	return &App{store: st, modules: set, api: a, handler: router}, nil
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

// Close releases the store.
func (a *App) Close() error { return a.store.Close() }

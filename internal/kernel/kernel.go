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
	"github.com/XMZO/JustEnoughCerts/internal/kernel/clientip"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/configfile"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/configfile/configfileapi"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers/providersapi"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/system"
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

	// restart closes when a backup waits to be restored, for the process
	// to stop and start again.
	restart chan struct{}
}

// Restarting closes when JEC has to stop to start again, as after a backup
// was staged to be restored.
func (a *App) Restarting() <-chan struct{} { return a.restart }

// New opens and migrates the store, loads which modules are on and lets
// every module register its parts.
func New(ctx context.Context, cfg config.Config, log *slog.Logger, mods []module.Module) (app *App, err error) {
	set, err := module.NewSet(mods...)
	if err != nil {
		return nil, err
	}
	// A backup restored through the API waits for this start.
	kept, err := store.ApplyRestore(cfg.DataDir, time.Now())
	if err != nil {
		return nil, err
	}
	if kept != "" {
		log.Warn("restored a backup; the database before it is kept", "file", kept)
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
	registry := providers.New(st.DB(), box.Scope(owner), enabled)

	cookiePath := cfg.BasePath()
	if cookiePath == "" {
		cookiePath = "/"
	}
	clients, err := clientip.New(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	accounts, err := auth.New(ctx, st.DB(), log, auth.Options{
		CookiePath: cookiePath,
		Secure:     strings.HasPrefix(cfg.BaseURL, "https://"),
		Secrets:    box.Scope(owner),
		Clients:    clients,
	})
	if err != nil {
		return nil, err
	}
	build := buildinfo.Get()
	var recorders []recorder
	a, err := api.New(api.Options{
		BasePath:      cfg.BasePath(),
		Origin:        origin(cfg.BaseURL),
		Version:       build.Version,
		Commit:        build.Commit,
		Date:          build.Date,
		Platform:      build.Platform,
		Middlewares:   []func(huma.Context, func(huma.Context)){accounts.Middleware, audit(set, cfg.BasePath()+api.Prefix, &recorders)},
		SessionCookie: auth.CookieName,
	}, set, log)
	if err != nil {
		return nil, err
	}
	accounts.Register(a.Kernel())
	providersapi.Register(a.Kernel(), registry)
	restart := make(chan struct{})
	var restartOnce sync.Once
	versions := map[string]int64{owner: store.LatestVersion(migrations.FS)}
	for _, m := range set.Modules() {
		versions[m.Manifest().ID] = store.LatestVersion(m.Migrations())
	}
	system.Register(a.Kernel(), system.Options{
		DataDir: cfg.DataDir, Listen: cfg.Listen, BaseURL: cfg.BaseURL, KeyFromEnv: cfg.SecretKey != "",
		TrustedProxies: cfg.TrustedProxies, HSTS: cfg.HSTS == "true",
		VerifyKey: box.Verify, Versions: versions, Restart: func() { restartOnce.Do(func() { close(restart) }) },
		DB: st.DB(), Confirm: accounts.ConfirmPassword, Now: time.Now, Log: log,
	})
	cfgFile := configfile.New(st.DB(), registry, enabled)
	configfileapi.Register(a.Kernel(), cfgFile, configfileapi.Options{Confirm: accounts.ConfirmPassword, Now: time.Now, Log: log})
	var wellKnown []wellKnownHandler
	for _, m := range set.Modules() {
		id := m.Manifest().ID
		r := &module.Registry{
			API:     a.Module(id, func() bool { return set.Enabled(id) }),
			DB:      st.DB(),
			Log:     log.With("module", id),
			Events:  bus.Scope(id),
			Jobs:    sched.Scope(id),
			Secrets: box.Scope(id),

			Providers: registry.Scope(id),

			BaseURL:         cfg.BaseURL,
			DataDir:         cfg.DataDir,
			ConfirmPassword: accounts.ConfirmPassword,
			WellKnown:       func(h http.Handler) { wellKnown = append(wellKnown, wellKnownHandler{module: id, h: h}) },
			Audit: func(record func(context.Context, module.Request)) {
				recorders = append(recorders, recorder{module: id, record: record})
			},
			Config: func(s configfile.Section) error { return cfgFile.Add(id, s) },
		}
		if err := m.Register(r); err != nil {
			return nil, fmt.Errorf("module %s: %w", id, err)
		}
	}
	if err := sched.Scope(owner).Add(jobs.Job{Name: "prune_events", Every: 24 * time.Hour, Run: bus.Prune}); err != nil {
		return nil, err
	}
	bus.Seal()
	registry.Seal()

	router := chi.NewRouter()
	router.Handle("/.well-known/*", serveWellKnown(set, wellKnown))
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
	return &App{store: st, modules: set, api: a, handler: router, bus: bus, jobs: sched, restart: restart}, nil
}

// recorder is a module's recorder of operations (module.Registry.Audit).
type recorder struct {
	module string
	record func(context.Context, module.Request)
}

// audit hands each operation that may change something, once answered, to
// the recorders of the modules that are on. It runs after the session
// check, so it knows who asked, and requests turned away there do not
// reach it. prefix is the path of the API, which the record leaves out.
func audit(set *module.Set, prefix string, recorders *[]recorder) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		next(ctx)
		method := ctx.Method()
		if method == http.MethodGet || method == http.MethodHead || len(*recorders) == 0 {
			return
		}
		req := module.Request{
			At: time.Now().UTC(), Method: method, Path: strings.TrimPrefix(ctx.URL().Path, prefix), Status: ctx.Status(),
			Client: auth.ClientFrom(ctx.Context()), UserAgent: ctx.Header("User-Agent"), Token: auth.TokenFrom(ctx.Context()),
		}
		if op := ctx.Operation(); op != nil {
			req.Operation = op.OperationID
		}
		if u, ok := auth.UserFrom(ctx.Context()); ok {
			req.User = u.Username
		} else {
			req.User = auth.SignedInAs(ctx.Context())
		}
		for _, r := range *recorders {
			if set.Enabled(r.module) {
				r.record(ctx.Context(), req)
			}
		}
	}
}

// wellKnownHandler is a module's handler of the requests under
// /.well-known/ (module.Registry.WellKnown).
type wellKnownHandler struct {
	module string
	h      http.Handler
}

// serveWellKnown hands a request under /.well-known/ to the handlers of
// the modules that are on, in order, until one answers with something
// other than 404.
func serveWellKnown(set *module.Set, handlers []wellKnownHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, wk := range handlers {
			if !set.Enabled(wk.module) {
				continue
			}
			t := &tryWriter{ResponseWriter: w}
			wk.h.ServeHTTP(t, r)
			if !t.missed {
				return
			}
			clear(w.Header())
		}
		http.NotFound(w, r)
	})
}

// tryWriter passes a response on, unless it starts with 404: its handler
// then had nothing to say, and what it writes is dropped.
type tryWriter struct {
	http.ResponseWriter
	wrote, missed bool
}

func (t *tryWriter) WriteHeader(code int) {
	if t.wrote {
		return
	}
	t.wrote = true
	if code == http.StatusNotFound {
		t.missed = true
		return
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *tryWriter) Write(p []byte) (int, error) {
	if !t.wrote {
		t.WriteHeader(http.StatusOK)
	}
	if t.missed {
		return len(p), nil
	}
	return t.ResponseWriter.Write(p)
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

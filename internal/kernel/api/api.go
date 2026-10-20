// Package api serves the HTTP API: the kernel's operations and those of every
// module, under <base path>/api/v1, with an OpenAPI document at
// /api/v1/openapi.json from which the web UI's types are generated.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Prefix is the path of this API version under the base path.
const Prefix = "/api/v1"

// SessionScheme names the security scheme of the session cookie in the
// OpenAPI document. Operations require it unless they are Public.
const SessionScheme = "session"

// Public is the Security of an operation that needs no session.
var Public = []map[string][]string{}

// Audited is the key, in the Metadata of a GET operation, that has the
// audit log record it all the same, as it does every operation that may
// change something: a sign-in through another site, which lands on a GET.
const Audited = "audited"

// AnyClient is the key, in the Metadata of an operation, that has it
// answer clients outside JEC_ALLOWED_CLIENTS too: one that machines use
// with a key of their own, as servers fetching a certificate or calendar
// apps fetching a feed, from wherever they are.
const AnyClient = "anyClient"

// Options configure the API.
type Options struct {
	// BasePath is the path the UI is served under, "" at the root.
	BasePath string
	// Origin is the scheme://host[:port] users open, if known; requests from
	// it pass the cross-origin check even when a proxy rewrites Host.
	Origin string
	// Version is the program version; Commit, Date and Platform tell the
	// build, as package buildinfo does.
	Version, Commit, Date, Platform string
	// Middlewares run for every operation. huma binds them when an operation
	// is registered, so they cannot be added later.
	Middlewares []func(huma.Context, func(huma.Context))
	// SessionCookie is the name of the session cookie, for the document.
	SessionCookie string
	// TimeZone returns the time zone chosen for JEC, or the server's, which
	// the manifest tells.
	TimeZone func() *time.Location
}

// API is the HTTP API.
type API struct {
	huma    huma.API
	handler http.Handler
}

// New builds the API for the modules in set.
func New(opts Options, set *module.Set, log *slog.Logger) (*API, error) {
	router := chi.NewRouter()
	router.Use(recoverer(log), noStore)
	router.NotFound(statusHandler(http.StatusNotFound))
	router.MethodNotAllowed(statusHandler(http.StatusMethodNotAllowed))

	cfg := huma.DefaultConfig("Just Enough Certs", opts.Version)
	cfg.Servers = []*huma.Server{{URL: opts.BasePath + Prefix}}
	cfg.OpenAPIPath = "/openapi"
	// The docs UI would load its scripts from a CDN, and the $schema links
	// add a field to every response; the OpenAPI document is enough.
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	cfg.CreateHooks = nil
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		SessionScheme: {Type: "apiKey", In: "cookie", Name: opts.SessionCookie},
	}
	cfg.Security = []map[string][]string{{SessionScheme: {}}}

	h := humachi.New(router, cfg)
	h.UseMiddleware(opts.Middlewares...)

	// Browsers send Sec-Fetch-Site or Origin with every request; a state
	// changing one from another site is refused before it reaches a handler.
	csrf := http.NewCrossOriginProtection()
	if opts.Origin != "" {
		if err := csrf.AddTrustedOrigin(opts.Origin); err != nil {
			return nil, err
		}
	}
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, NewError(http.StatusForbidden, i18n.KernelErrorCrossOrigin, nil))
	}))

	a := &API{huma: h, handler: csrf.Handler(router)}
	a.registerManifest(set, opts)
	a.registerSwitch(set, log)
	return a, nil
}

// Handler serves the API; mount it at base + Prefix.
func (a *API) Handler() http.Handler { return a.handler }

// Kernel returns the API the kernel registers its own operations with.
func (a *API) Kernel() huma.API { return a.huma }

// Module returns the API a module registers its operations with: their
// paths go under /<id>, and they answer 404 while enabled reports false.
func (a *API) Module(id string, enabled func() bool) huma.API {
	g := huma.NewGroup(a.huma, "/"+id)
	g.UseSimpleModifier(func(op *huma.Operation) {
		op.Tags = append(op.Tags, id)
	})
	g.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		if !enabled() {
			_ = huma.WriteErr(g, ctx, http.StatusNotFound, "module disabled")
			return
		}
		next(ctx)
	})
	return g
}

// ModuleInfo is what the manifest tells about a module.
type ModuleInfo struct {
	ID           string          `json:"id"`
	Category     module.Category `json:"category" enum:"source,solver,target,notifier,feature"`
	Name         i18n.Key        `json:"name" doc:"Message key of the module name"`
	Description  i18n.Key        `json:"description" doc:"Message key of the module description"`
	Requires     []string        `json:"requires,omitempty" doc:"Modules this one needs"`
	Required     bool            `json:"required,omitempty" doc:"Always on"`
	Experimental bool            `json:"experimental,omitempty" doc:"Off until switched on, and may break"`
	Switched     bool            `json:"switched" doc:"Switched on by itself"`
	Enabled      bool            `json:"enabled" doc:"Running: switched on, and every module it requires too"`
}

// ManifestBody is the response of GET /manifest.
type ManifestBody struct {
	Version  string       `json:"version" doc:"Program version"`
	Commit   string       `json:"commit,omitempty" doc:"The revision the program was built from"`
	Date     string       `json:"date,omitempty" doc:"RFC 3339 time of that revision"`
	Platform string       `json:"platform" doc:"GOOS/GOARCH, such as linux/amd64"`
	Modules  []ModuleInfo `json:"modules" doc:"Every module compiled in, by category and ID"`
	TimeZone string       `json:"timeZone,omitempty" doc:"The IANA name of the time zone the panel shows times in: the one chosen in the settings, or the server's; empty when the server tells none, and the browser's is used"`
}

func (a *API) registerManifest(set *module.Set, opts Options) {
	huma.Register(a.huma, huma.Operation{
		OperationID: "get-manifest",
		Method:      http.MethodGet,
		Path:        "/manifest",
		Summary:     "List the modules and their state",
		Tags:        []string{"kernel"},
	}, func(context.Context, *struct{}) (*struct{ Body ManifestBody }, error) {
		out := &struct{ Body ManifestBody }{Body: ManifestBody{
			Version: opts.Version, Commit: opts.Commit, Date: opts.Date, Platform: opts.Platform, Modules: []ModuleInfo{},
		}}
		if opts.TimeZone != nil {
			if name := opts.TimeZone().String(); name != "Local" {
				out.Body.TimeZone = name
			}
		}
		for _, st := range set.Statuses() {
			out.Body.Modules = append(out.Body.Modules, moduleInfo(st))
		}
		return out, nil
	})
}

func moduleInfo(st module.Status) ModuleInfo {
	return ModuleInfo{
		ID:           st.ID,
		Category:     st.Category,
		Name:         st.Name,
		Description:  st.Description,
		Requires:     st.Requires,
		Required:     st.Required,
		Experimental: st.Experimental,
		Switched:     st.Switched,
		Enabled:      st.Enabled,
	}
}

type switchInput struct {
	ID   string `path:"id"`
	Body struct {
		On bool `json:"on" doc:"Switch the module on, or off"`
	}
}

// registerSwitch adds PUT /modules/{id}, which switches a module on or off
// while JEC runs. Every module's tables exist already: the migrations of
// all modules run at startup.
func (a *API) registerSwitch(set *module.Set, log *slog.Logger) {
	huma.Register(a.huma, huma.Operation{
		OperationID: "switch-module",
		Method:      http.MethodPut,
		Path:        "/modules/{id}",
		Summary:     "Switch a module on or off",
		Description: "A module switched on runs once the modules it requires are on too. Required modules cannot be switched off.",
		Tags:        []string{"kernel"},
	}, func(ctx context.Context, in *switchInput) (*struct{ Body ModuleInfo }, error) {
		err := set.SetEnabled(ctx, in.ID, in.Body.On)
		switch {
		case errors.Is(err, module.ErrUnknown):
			return nil, NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		case errors.Is(err, module.ErrRequired):
			return nil, NewError(http.StatusConflict, i18n.KernelErrorModuleRequired, nil)
		case err != nil:
			return nil, err
		}
		log.Info("module switched", "module", in.ID, "on", in.Body.On)
		for _, st := range set.Statuses() {
			if st.ID == in.ID {
				return &struct{ Body ModuleInfo }{Body: moduleInfo(st)}, nil
			}
		}
		return nil, NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
	})
}

// recoverer turns a panic in a handler into a logged internal error.
func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v) // net/http aborts the response quietly
				}
				log.Error("api handler panicked", "method", r.Method, "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				writeError(w, NewError(http.StatusInternalServerError, i18n.KernelErrorInternal, nil))
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// noStore keeps API responses, which may hold secrets, out of caches.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

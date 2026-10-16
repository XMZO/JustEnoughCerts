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

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Prefix is the path of this API version under the base path.
const Prefix = "/api/v1"

// API is the HTTP API.
type API struct {
	huma   huma.API
	router chi.Router
}

// New builds the API for the modules in set. base is the path the UI is
// served under ("" at the root) and version the program version.
func New(base, version string, set *module.Set, log *slog.Logger) *API {
	router := chi.NewRouter()
	router.Use(recoverer(log), noStore)
	router.NotFound(statusHandler(http.StatusNotFound))
	router.MethodNotAllowed(statusHandler(http.StatusMethodNotAllowed))

	cfg := huma.DefaultConfig("Just Enough Certs", version)
	cfg.Servers = []*huma.Server{{URL: base + Prefix}}
	cfg.OpenAPIPath = "/openapi"
	// The docs UI would load its scripts from a CDN, and the $schema links
	// add a field to every response; the OpenAPI document is enough.
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	cfg.CreateHooks = nil

	a := &API{huma: humachi.New(router, cfg), router: router}
	a.registerManifest(set, version)
	return a
}

// Handler serves the API; mount it at base + Prefix.
func (a *API) Handler() http.Handler { return a.router }

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
	Version string       `json:"version" doc:"Program version"`
	Modules []ModuleInfo `json:"modules" doc:"Every module compiled in, by category and ID"`
}

func (a *API) registerManifest(set *module.Set, version string) {
	huma.Register(a.huma, huma.Operation{
		OperationID: "get-manifest",
		Method:      http.MethodGet,
		Path:        "/manifest",
		Summary:     "List the modules and their state",
		Tags:        []string{"kernel"},
	}, func(context.Context, *struct{}) (*struct{ Body ManifestBody }, error) {
		out := &struct{ Body ManifestBody }{Body: ManifestBody{Version: version, Modules: []ModuleInfo{}}}
		for _, st := range set.Statuses() {
			out.Body.Modules = append(out.Body.Modules, ModuleInfo{
				ID:           st.ID,
				Category:     st.Category,
				Name:         st.Name,
				Description:  st.Description,
				Requires:     st.Requires,
				Required:     st.Required,
				Experimental: st.Experimental,
				Switched:     st.Switched,
				Enabled:      st.Enabled,
			})
		}
		return out, nil
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

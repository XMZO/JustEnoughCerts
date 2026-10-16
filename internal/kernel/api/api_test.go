package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

type fakeModule struct{ mf module.Manifest }

func (f fakeModule) Manifest() module.Manifest     { return f.mf }
func (fakeModule) Migrations() fs.FS               { return nil }
func (fakeModule) Register(*module.Registry) error { return nil }

type nameInput struct {
	Body struct {
		Name string `json:"name" minLength:"3"`
	}
}

type okOutput struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

// setup serves an API with one module, "demo", whose operations are
// switched by the returned flag.
func setup(t *testing.T) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	set, err := module.NewSet(fakeModule{module.Manifest{
		ID: "demo", Category: module.Target, Name: "demo.module.name", Description: "demo.module.description",
	}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{BasePath: "/certs", Origin: "https://certs.example.com", Version: "1.2.3", SessionCookie: "s"},
		set, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	var on atomic.Bool
	on.Store(true)
	m := a.Module("demo", on.Load)
	huma.Register(m, huma.Operation{OperationID: "demo-create", Method: http.MethodPost, Path: "/things"},
		func(context.Context, *nameInput) (*okOutput, error) {
			out := &okOutput{}
			out.Body.OK = true
			return out, nil
		})
	huma.Register(m, huma.Operation{OperationID: "demo-fail", Method: http.MethodGet, Path: "/fail"},
		func(context.Context, *struct{}) (*okOutput, error) { return nil, errors.New("database is on fire") })
	huma.Register(m, huma.Operation{OperationID: "demo-panic", Method: http.MethodGet, Path: "/panic"},
		func(context.Context, *struct{}) (*okOutput, error) { panic("boom") })
	huma.Register(m, huma.Operation{OperationID: "demo-conflict", Method: http.MethodGet, Path: "/conflict"},
		func(context.Context, *struct{}) (*okOutput, error) {
			return nil, NewError(http.StatusConflict, "demo.error.taken", map[string]any{"name": "x"})
		})
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return srv, &on
}

func TestCrossOrigin(t *testing.T) {
	srv, _ := setup(t)
	send := func(headers map[string]string) reply {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/demo/things", strings.NewReader(`{"name": "long enough"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return reply{status: resp.StatusCode, header: resp.Header, body: data}
	}
	if r := send(map[string]string{"Sec-Fetch-Site": "cross-site"}); r.status != http.StatusForbidden || decodeError(t, r.body).Code != i18n.KernelErrorCrossOrigin {
		t.Errorf("cross-site POST = %d %s", r.status, r.body)
	}
	if r := send(map[string]string{"Origin": "https://evil.example"}); r.status != http.StatusForbidden {
		t.Errorf("POST from another origin = %d %s", r.status, r.body)
	}
	// The configured origin passes even when the Host header differs, as
	// behind a proxy; so do same-origin and non-browser requests.
	for _, h := range []map[string]string{{"Origin": "https://certs.example.com"}, {"Sec-Fetch-Site": "same-origin"}, {}} {
		if r := send(h); r.status != http.StatusOK {
			t.Errorf("POST with %v = %d %s", h, r.status, r.body)
		}
	}
}

// reply is a response with its body read and closed.
type reply struct {
	status int
	header http.Header
	body   []byte
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: data}
}

func decodeError(t *testing.T, data []byte) Error {
	t.Helper()
	var e Error
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("not an error body: %s", data)
	}
	return e
}

func TestErrors(t *testing.T) {
	srv, _ := setup(t)
	tests := []struct {
		method, path, body string
		status             int
		code               i18n.Key
	}{
		{http.MethodGet, "/nope", "", http.StatusNotFound, i18n.KernelErrorNotFound},
		{http.MethodDelete, "/manifest", "", http.StatusMethodNotAllowed, i18n.KernelErrorMethodNotAllowed},
		{http.MethodGet, "/demo/fail", "", http.StatusInternalServerError, i18n.KernelErrorInternal},
		{http.MethodGet, "/demo/panic", "", http.StatusInternalServerError, i18n.KernelErrorInternal},
		{http.MethodGet, "/demo/conflict", "", http.StatusConflict, "demo.error.taken"},
		{http.MethodPost, "/demo/things", "{not json", http.StatusBadRequest, i18n.KernelErrorInvalidRequest},
	}
	for _, tt := range tests {
		r := do(t, srv, tt.method, tt.path, tt.body)
		e := decodeError(t, r.body)
		if r.status != tt.status || e.Code != tt.code {
			t.Errorf("%s %s = %d %s, want %d %s", tt.method, tt.path, r.status, r.body, tt.status, tt.code)
		}
		if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s %s: Content-Type %q", tt.method, tt.path, ct)
		}
		if strings.Contains(string(r.body), "fire") || strings.Contains(string(r.body), "boom") {
			t.Errorf("%s %s leaks the internal error: %s", tt.method, tt.path, r.body)
		}
	}
}

func TestValidationError(t *testing.T) {
	srv, _ := setup(t)
	r := do(t, srv, http.MethodPost, "/demo/things", `{"name": "x"}`)
	e := decodeError(t, r.body)
	if r.status != http.StatusUnprocessableEntity || e.Code != i18n.KernelErrorInvalidRequest {
		t.Fatalf("= %d %s", r.status, r.body)
	}
	if len(e.Fields) != 1 || e.Fields[0].Location != "body.name" || e.Fields[0].Code != i18n.KernelErrorInvalidField {
		t.Errorf("fields = %+v", e.Fields)
	}
	if r := do(t, srv, http.MethodPost, "/demo/things", `{"name": "long enough"}`); !strings.Contains(string(r.body), `"ok":true`) {
		t.Errorf("a valid request failed: %s", r.body)
	}
}

func TestModuleSwitch(t *testing.T) {
	srv, on := setup(t)
	on.Store(false)
	r := do(t, srv, http.MethodPost, "/demo/things", `{"name": "long enough"}`)
	if r.status != http.StatusNotFound || decodeError(t, r.body).Code != i18n.KernelErrorNotFound {
		t.Errorf("switched-off module answered %d %s", r.status, r.body)
	}
	on.Store(true)
	if r := do(t, srv, http.MethodPost, "/demo/things", `{"name": "long enough"}`); r.status != http.StatusOK {
		t.Errorf("switched back on: %d", r.status)
	}
}

func TestManifest(t *testing.T) {
	srv, _ := setup(t)
	r := do(t, srv, http.MethodGet, "/manifest", "")
	if r.status != http.StatusOK || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("= %d, Cache-Control %q", r.status, r.header.Get("Cache-Control"))
	}
	var body ManifestBody
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "1.2.3" || len(body.Modules) != 1 || body.Modules[0].ID != "demo" || !body.Modules[0].Enabled {
		t.Errorf("manifest = %s", r.body)
	}
	if strings.Contains(string(r.body), "$schema") {
		t.Errorf("schema links are on: %s", r.body)
	}
}

func TestOpenAPI(t *testing.T) {
	srv, _ := setup(t)
	r := do(t, srv, http.MethodGet, "/openapi.json", "")
	if r.status != http.StatusOK {
		t.Fatalf("= %d", r.status)
	}
	var doc struct {
		Servers []struct{ URL string } `json:"servers"`
		Paths   map[string]any         `json:"paths"`
	}
	if err := json.Unmarshal(r.body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Servers) != 1 || doc.Servers[0].URL != "/certs/api/v1" {
		t.Errorf("servers = %+v", doc.Servers)
	}
	for _, p := range []string{"/manifest", "/demo/things"} {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("the document lacks %s", p)
		}
	}
	if r := do(t, srv, http.MethodGet, "/docs", ""); r.status != http.StatusNotFound {
		t.Errorf("the docs UI is served: %d", r.status)
	}
}

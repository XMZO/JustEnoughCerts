package providers_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers/providersapi"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

type fixture struct {
	t   *testing.T
	reg *providers.Registry
	db  *sql.DB
	box *secrets.Scope
	api humatest.TestAPI

	mu     sync.Mutex
	off    map[string]bool
	inUse  map[int64]bool
	opened []providers.Instance
}

// dnsType is a solver of module "dns" with one field of each sort.
var dnsType = providers.Type{
	ID: "dns.acme_dns", Kind: providers.Solver, Brand: "ACME DNS", URL: "https://example.com", Tags: []string{"self_hosted"},
	Fields: []providers.Field{
		{Key: "API_USER", Kind: providers.Text, Required: true},
		{Key: "API_KEY", Kind: providers.Text, Secret: true, Required: true},
		{Key: "TOKEN2", Kind: providers.Text, Secret: true},
		{Key: "TTL", Kind: providers.Number, Default: "120"},
		{Key: "ENDPOINT", Kind: providers.URL},
		{Key: "MAIL", Kind: providers.Email},
		{Key: "MODE", Kind: providers.Select, Options: []providers.Option{{Value: "fast", Label: "dns.mode.fast"}, {Value: "safe", Label: "dns.mode.safe"}}},
		{Key: "DEBUG", Kind: providers.Switch},
		{Key: "NOTE", Kind: providers.Multiline},
	},
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: st.DB(), box: box.Scope("kernel"), off: map[string]bool{}, inUse: map[int64]bool{}}
	f.reg = providers.New(f.db, f.box, func(m string) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return !f.off[m]
	})
	scope := f.reg.Scope("dns")
	if err := scope.Add(dnsType, func(_ context.Context, in providers.Instance) (any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.opened = append(f.opened, in)
		return "solver for " + in.Name, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.reg.Scope("mail").Add(providers.Type{ID: "mail.smtp", Kind: providers.Notifier, Name: "mail.type.name", Fields: []providers.Field{}},
		func(context.Context, providers.Instance) (any, error) { return nil, errors.New("not here") }); err != nil {
		t.Fatal(err)
	}
	f.reg.Scope("certs").UsedBy(func(_ context.Context, id int64) (bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.inUse[id], nil
	})
	f.reg.Seal()
	_, f.api = humatest.New(t)
	providersapi.Register(f.api, f.reg)
	return f
}

func (f *fixture) create(body map[string]any) providersapi.InstanceBody {
	f.t.Helper()
	resp := f.api.Post("/providers", body)
	if resp.Code != http.StatusCreated {
		f.t.Fatalf("create = %d %s", resp.Code, resp.Body.String())
	}
	var out providersapi.InstanceBody
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

type errorBody struct {
	Code   string `json:"code"`
	Fields []struct {
		Location string `json:"location"`
		Code     string `json:"code"`
	} `json:"fields"`
}

func (f *fixture) fails(method, path string, body any, status int) errorBody {
	f.t.Helper()
	var resp interface {
		Result() *http.Response
	}
	switch method {
	case http.MethodPost:
		resp = f.api.Post(path, body)
	case http.MethodPatch:
		resp = f.api.Patch(path, body)
	case http.MethodDelete:
		resp = f.api.Delete(path)
	default:
		resp = f.api.Get(path)
	}
	r := resp.Result()
	defer r.Body.Close()
	var e errorBody
	_ = json.NewDecoder(r.Body).Decode(&e)
	if r.StatusCode != status {
		f.t.Fatalf("%s %s = %d (%+v), want %d", method, path, r.StatusCode, e, status)
	}
	return e
}

func locations(e errorBody) string {
	var out []string
	for _, field := range e.Fields {
		out = append(out, field.Location+"="+field.Code)
	}
	return strings.Join(out, " ")
}

func TestAddChecks(t *testing.T) {
	f := newFixture(t)
	factory := func(context.Context, providers.Instance) (any, error) { return nil, nil }
	s := providers.New(f.db, f.box, func(string) bool { return true }).Scope("dns")
	if err := s.Add(providers.Type{ID: "dns.ok", Kind: providers.Solver, Brand: "OK"}, factory); err != nil {
		t.Fatal(err)
	}
	for name, typ := range map[string]providers.Type{
		"other module":   {ID: "web.x", Kind: providers.Solver, Brand: "X"},
		"no name":        {ID: "dns.x", Kind: providers.Solver},
		"bad name":       {ID: "dns.X", Kind: providers.Solver, Brand: "X"},
		"bad kind":       {ID: "dns.x", Kind: "dns", Brand: "X"},
		"repeated type":  {ID: "dns.ok", Kind: providers.Solver, Brand: "X"},
		"bad key":        {ID: "dns.x", Kind: providers.Solver, Brand: "X", Fields: []providers.Field{{Key: "a-b", Kind: providers.Text}}},
		"repeated key":   {ID: "dns.x", Kind: providers.Solver, Brand: "X", Fields: []providers.Field{{Key: "A", Kind: providers.Text}, {Key: "A", Kind: providers.Text}}},
		"bad field kind": {ID: "dns.x", Kind: providers.Solver, Brand: "X", Fields: []providers.Field{{Key: "A", Kind: "date"}}},
		"empty select":   {ID: "dns.x", Kind: providers.Solver, Brand: "X", Fields: []providers.Field{{Key: "A", Kind: providers.Select}}},
	} {
		if err := s.Add(typ, factory); err == nil {
			t.Errorf("%s: added", name)
		}
	}
	if err := s.Add(providers.Type{ID: "dns.y", Kind: providers.Solver, Brand: "Y"}, nil); err == nil {
		t.Error("added a type without a factory")
	}
	if err := f.reg.Scope("dns").Add(providers.Type{ID: "dns.late", Kind: providers.Solver, Brand: "L"}, factory); err == nil {
		t.Error("added a type after the registry was sealed")
	}
}

func TestTypes(t *testing.T) {
	f := newFixture(t)
	var out struct{ Types []providersapi.TypeSummary }
	resp := f.api.Get("/providers/types?kind=solver")
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil || len(out.Types) != 1 || out.Types[0].ID != "dns.acme_dns" || out.Types[0].Module != "dns" {
		t.Errorf("solver types: %s", resp.Body.String())
	}
	if resp := f.api.Get("/providers/types"); !strings.Contains(resp.Body.String(), "mail.smtp") {
		t.Errorf("all types: %s", resp.Body.String())
	}
	var full providers.Type
	if err := json.Unmarshal(f.api.Get("/providers/types/dns.acme_dns").Body.Bytes(), &full); err != nil || len(full.Fields) != len(dnsType.Fields) {
		t.Errorf("type detail: %+v %v", full, err)
	}
	f.fails(http.MethodGet, "/providers/types/dns.nope", nil, http.StatusNotFound)

	f.off["dns"] = true
	if resp := f.api.Get("/providers/types?kind=solver"); strings.Contains(resp.Body.String(), "dns.acme_dns") {
		t.Error("a type of a module that is off is listed")
	}
	f.fails(http.MethodGet, "/providers/types/dns.acme_dns", nil, http.StatusNotFound)
}

func TestCreateAndValidate(t *testing.T) {
	f := newFixture(t)
	e := f.fails(http.MethodPost, "/providers", map[string]any{"type": "dns.acme_dns", "name": "x",
		"config":  map[string]string{"TTL": "soon", "ENDPOINT": "ftp://x", "MAIL": "nobody", "MODE": "slow", "DEBUG": "yes", "API_KEY": "plain", "OTHER": "1", "NOTE": "a\nb"},
		"secrets": map[string]string{"API_USER": "secret?"},
	}, http.StatusUnprocessableEntity)
	want := "body.config.API_KEY=kernel.error.invalid_field body.config.API_USER=kernel.error.field_required " +
		"body.config.DEBUG=kernel.error.invalid_field body.config.ENDPOINT=kernel.error.invalid_field " +
		"body.config.MAIL=kernel.error.invalid_field body.config.MODE=kernel.error.invalid_field " +
		"body.config.OTHER=kernel.error.invalid_field body.config.TTL=kernel.error.invalid_field " +
		"body.secrets.API_KEY=kernel.error.field_required body.secrets.API_USER=kernel.error.invalid_field"
	if got := locations(e); got != want {
		t.Errorf("errors:\n got %s\nwant %s", got, want)
	}
	f.fails(http.MethodPost, "/providers", map[string]any{"type": "dns.nope", "name": "x"}, http.StatusUnprocessableEntity)
	f.fails(http.MethodPost, "/providers", map[string]any{"type": "dns.acme_dns", "name": ""}, http.StatusUnprocessableEntity)

	in := f.create(map[string]any{"type": "dns.acme_dns", "name": " main ",
		"config":  map[string]string{"API_USER": " admin ", "MODE": "safe", "DEBUG": "true", "MAIL": "a@example.com", "NOTE": "line 1\nline 2"},
		"secrets": map[string]string{"API_KEY": "s3cret", "TOKEN2": ""},
	})
	if in.Name != "main" || in.Kind != "solver" || in.Config["API_USER"] != "admin" || in.Config["TTL"] != "120" ||
		in.Config["NOTE"] != "line 1\nline 2" || strings.Join(in.SecretKeys, ",") != "API_KEY" {
		t.Errorf("created %+v", in)
	}
	var sealed []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT secrets FROM kernel_providers WHERE id = ?", in.ID).Scan(&sealed); err != nil || strings.Contains(string(sealed), "s3cret") {
		t.Errorf("secrets stored in the clear: %q %v", sealed, err)
	}
	if resp := f.api.Get("/providers/" + strconv.FormatInt(in.ID, 10)); strings.Contains(resp.Body.String(), "s3cret") {
		t.Error("the API sent a secret back")
	}
}

func TestUpdate(t *testing.T) {
	f := newFixture(t)
	in := f.create(map[string]any{"type": "dns.acme_dns", "name": "main",
		"config": map[string]string{"API_USER": "admin"}, "secrets": map[string]string{"API_KEY": "k1", "TOKEN2": "t1"}})
	path := "/providers/" + strconv.FormatInt(in.ID, 10)

	resp := f.api.Patch(path, map[string]any{"name": "backup", "secrets": map[string]string{"API_KEY": "k2", "TOKEN2": ""}})
	if resp.Code != http.StatusOK {
		t.Fatalf("update = %d %s", resp.Code, resp.Body.String())
	}
	_, opened, err := f.reg.Scope("certs").Open(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Name != "backup" || opened.Secrets["API_KEY"] != "k2" || len(opened.Secrets) != 1 || opened.Config["API_USER"] != "admin" {
		t.Errorf("after the update: %+v", opened)
	}
	resp = f.api.Patch(path, map[string]any{"config": map[string]string{"API_USER": "root", "MODE": "fast"}})
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"MODE":"fast"`) {
		t.Errorf("config update = %d %s", resp.Code, resp.Body.String())
	}
	e := f.fails(http.MethodPatch, path, map[string]any{"secrets": map[string]string{"API_KEY": ""}}, http.StatusUnprocessableEntity)
	if locations(e) != "body.secrets.API_KEY=kernel.error.field_required" {
		t.Errorf("removing a required secret: %+v", e)
	}
	f.fails(http.MethodPatch, "/providers/999", map[string]any{"name": "x"}, http.StatusNotFound)
}

func TestDeleteAndOpen(t *testing.T) {
	f := newFixture(t)
	in := f.create(map[string]any{"type": "dns.acme_dns", "name": "main",
		"config": map[string]string{"API_USER": "admin"}, "secrets": map[string]string{"API_KEY": "k"}})
	path := "/providers/" + strconv.FormatInt(in.ID, 10)
	scope := f.reg.Scope("certs")

	p, opened, err := scope.Open(t.Context(), in.ID)
	if err != nil || p != "solver for main" || opened.Value("API_KEY") != "k" || opened.Value("API_USER") != "admin" {
		t.Errorf("Open = %v, %+v, %v", p, opened, err)
	}
	if typ, err := scope.Type(t.Context(), in.ID); err != nil || typ.Kind != providers.Solver {
		t.Errorf("Type = %+v, %v", typ, err)
	}
	if _, _, err := scope.Open(t.Context(), 999); !errors.Is(err, providers.ErrNotFound) {
		t.Errorf("Open of no instance: %v", err)
	}

	f.inUse[in.ID] = true
	if e := f.fails(http.MethodDelete, path, nil, http.StatusConflict); e.Code != "kernel.error.provider_in_use" {
		t.Errorf("deleting an instance in use: %+v", e)
	}
	f.inUse[in.ID] = false

	f.off["dns"] = true
	if _, _, err := scope.Open(t.Context(), in.ID); !errors.Is(err, providers.ErrUnavailable) {
		t.Errorf("Open while the module is off: %v", err)
	}
	var list struct{ Providers []providersapi.InstanceBody }
	_ = json.Unmarshal(f.api.Get("/providers").Body.Bytes(), &list)
	if len(list.Providers) != 1 || list.Providers[0].Kind != "" {
		t.Errorf("an instance of a module that is off: %+v", list.Providers)
	}
	f.off["dns"] = false

	if resp := f.api.Delete(path); resp.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", resp.Code, resp.Body.String())
	}
	f.fails(http.MethodDelete, path, nil, http.StatusNotFound)
	if len(f.opened) != 1 {
		t.Errorf("the factory ran %d times, want once", len(f.opened))
	}
}

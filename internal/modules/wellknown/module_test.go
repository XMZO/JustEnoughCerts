package wellknown

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

func TestManifest(t *testing.T) {
	if _, err := module.NewSet(New()); err != nil {
		t.Fatal(err)
	}
}

func newModule(t *testing.T) *Module {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New()
	if _, err := st.Migrate(t.Context(), "wellknown", m.Migrations()); err != nil {
		t.Fatal(err)
	}
	m.db = st.DB()
	return m
}

// get asks the module's handler for path on host.
func get(m *Module, method, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+host+path, nil)
	rec := httptest.NewRecorder()
	m.serve(rec, req)
	return rec
}

func problemKey(err error) i18n.Key {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p.Key
	}
	return ""
}

func TestAnswer(t *testing.T) {
	m := newModule(t)
	a := &answer{m: m}
	path := "/.well-known/acme-challenge/token-1"
	if err := a.PutFile(t.Context(), contracts.File{Domain: "Example.com", Path: path, Content: []byte("key-auth")}); err != nil {
		t.Fatal(err)
	}
	rec := get(m, http.MethodGet, "example.com:80", path)
	if rec.Code != http.StatusOK || rec.Body.String() != "key-auth" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("= %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	// A proxy that changed the host still gets the answer, by its path.
	if rec := get(m, http.MethodGet, "jec.internal:8080", path); rec.Body.String() != "key-auth" {
		t.Errorf("other host = %d %q", rec.Code, rec.Body.String())
	}
	// The same path for another domain: each host gets its own.
	if err := a.PutFile(t.Context(), contracts.File{Domain: "other.example", Path: path, Content: []byte("other")}); err != nil {
		t.Fatal(err)
	}
	if rec := get(m, http.MethodGet, "other.example", path); rec.Body.String() != "other" {
		t.Errorf("other.example = %q", rec.Body.String())
	}
	if rec := get(m, http.MethodHead, "example.com", path); rec.Code != http.StatusOK {
		t.Errorf("HEAD = %d", rec.Code)
	}
	for _, rec := range []*httptest.ResponseRecorder{
		get(m, http.MethodGet, "example.com", "/.well-known/acme-challenge/unknown"),
		get(m, http.MethodPost, "example.com", path),
	} {
		if rec.Code != http.StatusNotFound {
			t.Errorf("= %d %q", rec.Code, rec.Body.String())
		}
	}

	for range 2 { // gone already the second time
		if err := a.RemoveFile(t.Context(), contracts.File{Domain: "example.com", Path: path}); err != nil {
			t.Fatal(err)
		}
	}
	if rec := get(m, http.MethodGet, "example.com", path); rec.Body.String() != "other" {
		t.Errorf("after the removal, by path = %q", rec.Body.String())
	}

	err := a.PutFile(t.Context(), contracts.File{Domain: "example.com", Path: "/index.html"})
	if problemKey(err) != i18n.WellknownErrorFilePath || !retry.IsPermanent(err) {
		t.Errorf("outside /.well-known/: %v", err)
	}
}

// roundTrip lets the module's own handler stand for the internet, as when
// the website's proxy sends /.well-known/ on to JEC.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTest(t *testing.T) {
	m := newModule(t)
	if _, err := (&answer{m: m}).Test(t.Context()); problemKey(err) != i18n.WellknownErrorNoDomain || !retry.IsPermanent(err) {
		t.Errorf("no domain: %v", err)
	}

	a := &answer{m: m, domain: "example.com"}
	m.web = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		m.serve(rec, r)
		return rec.Result(), nil
	})}
	msg, err := a.Test(t.Context())
	if err != nil || msg.Key != i18n.WellknownTestOk || !strings.HasPrefix(msg.Params["url"].(string), "http://example.com/.well-known/jec-check/") {
		t.Fatalf("reached: %v %v", msg, err)
	}
	var n int
	if err := m.db.QueryRowContext(t.Context(), "SELECT count(*) FROM wellknown_files").Scan(&n); err != nil || n != 0 {
		t.Errorf("%d files left, %v", n, err)
	}

	// The website answers itself: the requests do not reach JEC.
	m.web = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		http.NotFound(rec, r)
		return rec.Result(), nil
	})}
	if _, err := a.Test(t.Context()); problemKey(err) != i18n.WellknownErrorNotReached {
		t.Errorf("not reached: %v", err)
	}
}

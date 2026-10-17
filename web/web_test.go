package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            {Data: []byte(`<html><head><base href="/" /><script src="./assets/app-1a2b.js"></script></head></html>`)},
		"index.html.br":         {Data: []byte("stale compressed page")},
		"favicon.svg":           {Data: []byte("<svg/>")},
		"assets/app-1a2b.js":    {Data: []byte("console.log(1)")},
		"assets/app-1a2b.js.br": {Data: []byte("BR")},
		"assets/app-1a2b.js.gz": {Data: []byte("GZ")},
		".gitkeep":              {Data: nil},
	}
}

func get(t *testing.T, h http.Handler, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServe(t *testing.T) {
	h, err := NewHandler(testFS(), "/jec")
	if err != nil {
		t.Fatal(err)
	}

	// Routes of the UI get the page, rewritten for the base path.
	for _, p := range []string{"/jec/", "/jec/login", "/jec/certs/42", "/jec/index.html"} {
		rec := get(t, h, p, map[string]string{"Accept-Encoding": "br"})
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `<base href="/jec/">`) || rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("GET %s = %d %q, encoding %q", p, rec.Code, body, rec.Header().Get("Content-Encoding"))
		}
		if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Cache-Control") != "no-cache" ||
			rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("GET %s headers = %v", p, rec.Header())
		}
	}

	// Assets: the best encoding the client takes, cached for good.
	for _, tt := range []struct{ accept, encoding, body string }{
		{"gzip, deflate, br", "br", "BR"},
		{"gzip", "gzip", "GZ"},
		{"br;q=0, gzip;q=0.5", "gzip", "GZ"},
		{"", "", "console.log(1)"},
	} {
		rec := get(t, h, "/jec/assets/app-1a2b.js", map[string]string{"Accept-Encoding": tt.accept})
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != tt.encoding || rec.Body.String() != tt.body {
			t.Errorf("Accept-Encoding %q = %d %q %q", tt.accept, rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || rec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
			t.Errorf("asset headers = %v", rec.Header())
		}
	}

	// Revalidation: each encoding has its own tag.
	first := get(t, h, "/jec/assets/app-1a2b.js", map[string]string{"Accept-Encoding": "br"})
	if again := get(t, h, "/jec/assets/app-1a2b.js", map[string]string{"Accept-Encoding": "br", "If-None-Match": first.Header().Get("ETag")}); again.Code != http.StatusNotModified {
		t.Errorf("If-None-Match = %d", again.Code)
	}
	if plain := get(t, h, "/jec/assets/app-1a2b.js", map[string]string{"If-None-Match": first.Header().Get("ETag")}); plain.Code != http.StatusOK {
		t.Errorf("the br tag matched the plain file: %d", plain.Code)
	}

	for _, p := range []string{"/jec/assets/missing.js", "/jec/.gitkeep", "/other", "/jec/assets/app-1a2b.js.br"} {
		if rec := get(t, h, p, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}
	if rec := get(t, h, "/jec", nil); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/jec/" {
		t.Errorf("GET /jec = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	post := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/jec/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
}

func TestServeAtRoot(t *testing.T) {
	h, err := NewHandler(testFS(), "")
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(t, h, "/login", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<base href="/">`) {
		t.Errorf("GET /login = %d %q", rec.Code, rec.Body.String())
	}
}

func TestNotBuilt(t *testing.T) {
	if _, err := NewHandler(fstest.MapFS{".gitkeep": {}}, ""); !errors.Is(err, ErrNotBuilt) {
		t.Errorf("an empty dist = %v, want ErrNotBuilt", err)
	}
	if _, err := NewHandler(fstest.MapFS{"index.html": {Data: []byte("x")}, "a.js.br": {Data: []byte("x")}}, ""); err == nil {
		t.Error("a compressed file without its original was accepted")
	}
}

func TestAccepts(t *testing.T) {
	for _, tt := range []struct {
		header, enc string
		want        bool
	}{
		{"br", "br", true},
		{"gzip, br", "br", true},
		{"BR", "br", true},
		{"br;q=0", "br", false},
		{"br; q=0.0", "br", false},
		{"br;q=0.1", "br", true},
		{"gzip", "br", false},
		{"", "br", false},
	} {
		if got := accepts(tt.header, tt.enc); got != tt.want {
			t.Errorf("accepts(%q, %q) = %v", tt.header, tt.enc, got)
		}
	}
}

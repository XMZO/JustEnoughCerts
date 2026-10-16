package kernel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// notes is a small module: one table and an operation counting its rows.
type notes struct{}

func (notes) Manifest() module.Manifest {
	return module.Manifest{ID: "notes", Category: module.Feature, Name: "notes.module.name", Description: "notes.module.description"}
}

func (notes) Migrations() fs.FS {
	return fstest.MapFS{"00001_notes.sql": {Data: []byte("-- +goose Up\nCREATE TABLE notes_items (id INTEGER PRIMARY KEY) STRICT;\nINSERT INTO notes_items VALUES (1), (2);\n")}}
}

type countOutput struct {
	Body struct {
		Count int `json:"count"`
	}
}

func (notes) Register(r *module.Registry) error {
	huma.Register(r.API, huma.Operation{OperationID: "notes-count", Method: http.MethodGet, Path: "/count"},
		func(ctx context.Context, _ *struct{}) (*countOutput, error) {
			out := &countOutput{}
			err := r.DB.QueryRowContext(ctx, "SELECT count(*) FROM notes_items").Scan(&out.Body.Count)
			return out, err
		})
	return nil
}

// syncBuffer collects log output from concurrent handlers.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuffer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}

type client struct {
	t      *testing.T
	srv    *httptest.Server
	cookie string
}

// call sends a request, with the session cookie once there is one, and
// keeps the cookie the server sets.
func (c *client) call(method, path, body string) (int, string) {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.srv.URL+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: c.cookie})
	}
	resp, err := c.srv.Client().Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	for _, ck := range resp.Cookies() {
		if ck.Name == auth.CookieName {
			c.cookie = ck.Value
		}
	}
	return resp.StatusCode, string(data)
}

var setupToken = regexp.MustCompile(`setup_token=(\w+)`)

func TestApp(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.BaseURL = "https://example.com/jec"
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	app, err := New(t.Context(), cfg, log, []module.Module{notes{}})
	if err != nil {
		t.Fatal(err)
	}
	m := setupToken.FindStringSubmatch(logs.String())
	if m == nil {
		t.Fatalf("no setup token in the log:\n%s", logs.String())
	}
	c := &client{t: t, srv: httptest.NewServer(app.Handler())}
	if status, _ := c.call(http.MethodGet, "/jec/api/v1/notes/count", ""); status != http.StatusUnauthorized {
		t.Errorf("without a session: %d", status)
	}
	if status, body := c.call(http.MethodPost, "/jec/api/v1/setup", `{"token": "`+m[1]+`", "username": "admin", "password": "correct horse"}`); status != http.StatusCreated || c.cookie == "" {
		t.Fatalf("setup = %d %s", status, body)
	}
	if status, body := c.call(http.MethodGet, "/jec/api/v1/notes/count", ""); status != http.StatusOK || body != "{\"count\":2}\n" {
		t.Errorf("notes/count = %d %s", status, body)
	}
	if err := app.Modules().SetEnabled(t.Context(), "notes", false); err != nil {
		t.Fatal(err)
	}
	if status, _ := c.call(http.MethodGet, "/jec/api/v1/notes/count", ""); status != http.StatusNotFound {
		t.Errorf("switched-off module answered %d", status)
	}
	c.srv.Close()
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}

	// A restart keeps the account, the data and the module state, asks for
	// no setup and migrates nothing twice.
	logs.Reset()
	app, err = New(t.Context(), cfg, log, []module.Module{notes{}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if strings.Contains(logs.String(), "setup_token") {
		t.Errorf("setup token logged although an account exists:\n%s", logs.String())
	}
	c = &client{t: t, srv: httptest.NewServer(app.Handler())}
	defer c.srv.Close()
	if status, body := c.call(http.MethodPost, "/jec/api/v1/session", `{"username": "admin", "password": "correct horse"}`); status != http.StatusOK {
		t.Fatalf("sign in = %d %s", status, body)
	}
	status, body := c.call(http.MethodGet, "/jec/api/v1/manifest", "")
	var manifest struct {
		Modules []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(body), &manifest); err != nil || status != http.StatusOK || len(manifest.Modules) != 1 || manifest.Modules[0].Enabled {
		t.Errorf("manifest after restart = %d %s", status, body)
	}
}

func TestBadModules(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	if _, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler), []module.Module{notes{}, notes{}}); err == nil {
		t.Error("a module registered twice was accepted")
	}
}

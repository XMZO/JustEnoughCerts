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
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
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

// writer publishes note.added for each note it adds, in the transaction
// that adds it; reader collects those events.
type writer struct{}

func (writer) Manifest() module.Manifest {
	return module.Manifest{ID: "writer", Category: module.Feature, Name: "writer.module.name", Description: "writer.module.description"}
}

func (writer) Migrations() fs.FS {
	return fstest.MapFS{"00001_writer.sql": {Data: []byte("-- +goose Up\nCREATE TABLE writer_notes (id INTEGER PRIMARY KEY, text TEXT NOT NULL) STRICT;\n")}}
}

type addInput struct {
	Body struct {
		Text string `json:"text"`
	}
}

func (writer) Register(r *module.Registry) error {
	huma.Register(r.API, huma.Operation{OperationID: "writer-add", Method: http.MethodPost, Path: "/notes", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *addInput) (*struct{}, error) {
			tx, err := r.DB.BeginTx(ctx, nil)
			if err != nil {
				return nil, err
			}
			defer func() { _ = tx.Rollback() }()
			var id int64
			if err := tx.QueryRowContext(ctx, "INSERT INTO writer_notes (text) VALUES (?) RETURNING id", in.Body.Text).Scan(&id); err != nil {
				return nil, err
			}
			if err := r.Events.PublishTx(ctx, tx, "note.added", strconv.FormatInt(id, 10), map[string]string{"text": in.Body.Text}); err != nil {
				return nil, err
			}
			return nil, tx.Commit()
		})
	return nil
}

type reader struct{ got chan events.Event }

func (reader) Manifest() module.Manifest {
	return module.Manifest{ID: "reader", Category: module.Notifier, Name: "reader.module.name", Description: "reader.module.description"}
}

func (reader) Migrations() fs.FS { return nil }

func (m reader) Register(r *module.Registry) error {
	return r.Events.Subscribe(events.Subscription{Name: "notes", Types: []string{"note.*"}, Handler: func(_ context.Context, ev events.Event) error {
		m.got <- ev
		return nil
	}})
}

func TestEvents(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	var logs syncBuffer
	got := make(chan events.Event, 1)
	app, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(&logs, nil)), []module.Module{writer{}, reader{got}})
	if err != nil {
		t.Fatal(err)
	}
	app.Start()
	c := &client{t: t, srv: httptest.NewServer(app.Handler())}
	defer c.srv.Close()
	m := setupToken.FindStringSubmatch(logs.String())
	if m == nil {
		t.Fatal("no setup token in the log")
	}
	if status, body := c.call(http.MethodPost, "/api/v1/setup", `{"token": "`+m[1]+`", "username": "admin", "password": "correct horse"}`); status != http.StatusCreated {
		t.Fatalf("setup = %d %s", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/v1/writer/notes", `{"text": "renew example.com"}`); status != http.StatusNoContent {
		t.Fatalf("add a note = %d %s", status, body)
	}
	select {
	case ev := <-got:
		var data struct{ Text string }
		if err := ev.Decode(&data); err != nil || ev.Type != "note.added" || ev.Source != "writer" || ev.Subject != "1" || data.Text != "renew example.com" {
			t.Errorf("reader got %+v (%v)", ev, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the event did not reach the subscriber")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
}

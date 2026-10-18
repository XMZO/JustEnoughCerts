package kernel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	"github.com/XMZO/JustEnoughCerts/internal/kernel/configfile"
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

// answerer is a module answering one path under /.well-known/, or every
// path when path is "".
type answerer struct{ id, path string }

func (a answerer) Manifest() module.Manifest {
	return module.Manifest{ID: a.id, Category: module.Solver, Name: "x.module.name", Description: "x.module.description"}
}

func (answerer) Migrations() fs.FS { return nil }

func (a answerer) Register(r *module.Registry) error {
	r.WellKnown(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if a.path != "" && req.URL.Path != a.path {
			w.Header().Set("X-Answerer", a.id) // dropped with the 404
			http.NotFound(w, req)
			return
		}
		_, _ = io.WriteString(w, a.id)
	}))
	return nil
}

func TestWellKnown(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.BaseURL = "https://example.com/jec"
	app, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler), []module.Module{
		answerer{id: "first", path: "/.well-known/acme-challenge/token"}, answerer{id: "second"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.Modules().SetEnabled(t.Context(), "first", true); err != nil {
		t.Fatal(err)
	}
	if err := app.Modules().SetEnabled(t.Context(), "second", true); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	get := func(path string) (int, string, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data), resp.Header.Get("X-Answerer")
	}

	// At the root of the host, with no sign-in, whatever the base path.
	if status, body, _ := get("/.well-known/acme-challenge/token"); status != http.StatusOK || body != "first" {
		t.Errorf("first's path = %d %q", status, body)
	}
	if status, body, header := get("/.well-known/other"); status != http.StatusOK || body != "second" || header != "" {
		t.Errorf("past first's 404 = %d %q, header %q", status, body, header)
	}
	if err := app.Modules().SetEnabled(t.Context(), "second", false); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := get("/.well-known/other"); status != http.StatusNotFound {
		t.Errorf("with second off = %d", status)
	}
	if status, body, _ := get("/.well-known/acme-challenge/token"); status != http.StatusOK || body != "first" {
		t.Errorf("first's path, second off = %d %q", status, body)
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

func TestSecretKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	app, err := New(t.Context(), cfg, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "created the secret key file") {
		t.Errorf("no word of the new key file:\n%s", logs.String())
	}

	// The key file is used again on restart...
	app, err = New(t.Context(), cfg, log, nil)
	if err != nil {
		t.Fatalf("restart with the same key file: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	// ...and another key is refused, before any secret is needed.
	cfg.SecretKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if _, err := New(t.Context(), cfg, log, nil); err == nil || !strings.Contains(err.Error(), "secret key is not the one") {
		t.Errorf("restart with another key: %v", err)
	}
}

// auditor is a module that records the operations of the API, and has one
// of its own.
type auditor struct {
	mu   *sync.Mutex
	seen *[]module.Request
}

func (auditor) Manifest() module.Manifest {
	return module.Manifest{ID: "auditor", Category: module.Feature, Name: "auditor.module.name", Description: "auditor.module.description"}
}

func (auditor) Migrations() fs.FS { return nil }

func (a auditor) Register(r *module.Registry) error {
	r.Audit(func(_ context.Context, req module.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		*a.seen = append(*a.seen, req)
	})
	huma.Register(r.API, huma.Operation{OperationID: "auditor-poke", Method: http.MethodPost, Path: "/poke", DefaultStatus: http.StatusNoContent},
		func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })
	return nil
}

func TestAudit(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.BaseURL = "https://example.com/jec"
	var logs syncBuffer
	var mu sync.Mutex
	var seen []module.Request
	app, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(&logs, nil)), []module.Module{auditor{mu: &mu, seen: &seen}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.Modules().SetEnabled(t.Context(), "auditor", true); err != nil {
		t.Fatal(err)
	}
	m := setupToken.FindStringSubmatch(logs.String())
	if m == nil {
		t.Fatalf("no setup token in the log:\n%s", logs.String())
	}
	c := &client{t: t, srv: httptest.NewServer(app.Handler())}
	defer c.srv.Close()
	took := func() []module.Request {
		mu.Lock()
		defer mu.Unlock()
		out := seen
		seen = nil
		return out
	}

	// Turned away for want of a session: not recorded.
	if status, _ := c.call(http.MethodPost, "/jec/api/v1/auditor/poke", ""); status != http.StatusUnauthorized {
		t.Fatalf("poke without a session = %d", status)
	}
	if got := took(); len(got) != 0 {
		t.Errorf("recorded without a session: %+v", got)
	}
	if status, body := c.call(http.MethodPost, "/jec/api/v1/setup", `{"token": "`+m[1]+`", "username": "admin", "password": "correct horse"}`); status != http.StatusCreated {
		t.Fatalf("setup = %d %s", status, body)
	}
	c.call(http.MethodGet, "/jec/api/v1/manifest", "")
	c.call(http.MethodPost, "/jec/api/v1/auditor/poke", "")
	got := took()
	if len(got) != 2 || got[0].Operation != "setup" || got[0].Status != http.StatusCreated || got[0].Path != "/setup" || got[0].User != "admin" {
		t.Fatalf("recorded %+v", got)
	}
	if p := got[1]; p.Operation != "auditor-poke" || p.Method != http.MethodPost || p.Path != "/auditor/poke" || p.Status != http.StatusNoContent ||
		p.User != "admin" || p.Client != "127.0.0.1" || p.UserAgent == "" || p.At.IsZero() {
		t.Errorf("poke recorded as %+v", p)
	}

	// A failed sign-in, by no one; a sign-in, by whom it signed in.
	c.call(http.MethodDelete, "/jec/api/v1/session", "")
	c.call(http.MethodPost, "/jec/api/v1/session", `{"username": "admin", "password": "wrong horse"}`)
	c.call(http.MethodPost, "/jec/api/v1/session", `{"username": "admin", "password": "correct horse"}`)
	got = took()
	if len(got) != 3 || got[1].Operation != "sign-in" || got[1].Status != http.StatusUnauthorized || got[1].User != "" ||
		got[2].Status != http.StatusOK || got[2].User != "admin" {
		t.Errorf("sign-out and sign-ins recorded as %+v", got)
	}

	// Switched off, the module records nothing.
	if err := app.Modules().SetEnabled(t.Context(), "auditor", false); err != nil {
		t.Fatal(err)
	}
	c.call(http.MethodPost, "/jec/api/v1/session", `{"username": "admin", "password": "wrong horse"}`)
	if got := took(); len(got) != 0 {
		t.Errorf("recorded while off: %+v", got)
	}
}

// upload posts a multipart form with a file and a password.
func (c *client) upload(path string, file []byte, password string) (int, string) {
	c.t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "jec-backup.db")
	if err != nil {
		c.t.Fatal(err)
	}
	_, _ = part.Write(file)
	_ = w.WriteField("password", password)
	_ = w.Close()
	req, err := http.NewRequestWithContext(c.t.Context(), http.MethodPost, c.srv.URL+path, &body)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: c.cookie})
	resp, err := c.srv.Client().Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func TestRestore(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	app, err := New(t.Context(), cfg, log, []module.Module{notes{}})
	if err != nil {
		t.Fatal(err)
	}
	m := setupToken.FindStringSubmatch(logs.String())
	c := &client{t: t, srv: httptest.NewServer(app.Handler())}
	if status, body := c.call(http.MethodPost, "/api/v1/setup", `{"token": "`+m[1]+`", "username": "admin", "password": "correct horse"}`); status != http.StatusCreated {
		t.Fatalf("setup = %d %s", status, body)
	}
	status, backup := c.call(http.MethodPost, "/api/v1/system/backup", `{"password": "correct horse"}`)
	if status != http.StatusOK || !strings.HasPrefix(backup, "SQLite format 3") {
		t.Fatalf("backup = %d", status)
	}
	// After the backup, a token comes, which the restore takes away.
	if status, body := c.call(http.MethodPost, "/api/v1/account/tokens", `{"name": "later", "scope": "read", "password": "correct horse"}`); status != http.StatusCreated {
		t.Fatalf("token = %d %s", status, body)
	}

	for _, bad := range []struct {
		file, password string
		want           int
	}{
		{backup, "wrong horse", http.StatusUnprocessableEntity},
		{"not a backup at all, just some text that is long enough to read", "correct horse", http.StatusUnprocessableEntity},
		{backup[:len(backup)/2], "correct horse", http.StatusUnprocessableEntity},
	} {
		if status, body := c.upload("/api/v1/system/restore", []byte(bad.file), bad.password); status != bad.want {
			t.Errorf("restore of %d bytes with %q = %d %s", len(bad.file), bad.password, status, body)
		}
	}
	select {
	case <-app.Restarting():
		t.Fatal("a refused backup stops JEC")
	default:
	}

	if status, body := c.upload("/api/v1/system/restore", []byte(backup), "correct horse"); status != http.StatusAccepted {
		t.Fatalf("restore = %d %s", status, body)
	}
	select {
	case <-app.Restarting():
	case <-time.After(5 * time.Second):
		t.Fatal("JEC does not stop to restore")
	}
	c.srv.Close()
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}

	// Started again, JEC has the backup's data, and keeps what it had.
	app, err = New(t.Context(), cfg, log, []module.Module{notes{}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	c = &client{t: t, srv: httptest.NewServer(app.Handler())}
	defer c.srv.Close()
	if status, body := c.call(http.MethodPost, "/api/v1/session", `{"username": "admin", "password": "correct horse"}`); status != http.StatusOK {
		t.Fatalf("sign-in after the restore = %d %s", status, body)
	}
	if status, body := c.call(http.MethodGet, "/api/v1/account/tokens", ""); status != http.StatusOK || strings.Contains(body, "later") {
		t.Errorf("tokens after the restore = %d %s", status, body)
	}
	kept, _ := filepath.Glob(filepath.Join(cfg.DataDir, "jec.db.before-restore-*"))
	if len(kept) == 0 || !strings.Contains(logs.String(), "restored a backup") {
		t.Errorf("kept %v; log:\n%s", kept, logs.String())
	}
}

// keeper is a module with a section of the configuration file: a color it
// keeps in a table.
type keeper struct{}

func (keeper) Manifest() module.Manifest {
	return module.Manifest{ID: "keeper", Category: module.Feature, Name: "keeper.module.name", Description: "keeper.module.description"}
}

func (keeper) Migrations() fs.FS {
	return fstest.MapFS{"00001_keeper.sql": {Data: []byte("-- +goose Up\nCREATE TABLE keeper_color (color TEXT NOT NULL) STRICT;\nINSERT INTO keeper_color VALUES ('red');\n")}}
}

type keeperEntry struct {
	Color string `yaml:"color"`
}

func (keeper) Register(r *module.Registry) error {
	return r.Config(configfile.Section{
		Key: "keeper", Title: "keeper.config.title", New: func() any { return new(keeperEntry) },
		Export: func(ctx context.Context, _ configfile.Export) (any, error) {
			e := &keeperEntry{}
			return e, r.DB.QueryRowContext(ctx, "SELECT color FROM keeper_color").Scan(&e.Color)
		},
		Import: func(ctx context.Context, tx *sql.Tx, v any, in *configfile.Import) (func(), error) {
			var had string
			if err := tx.QueryRowContext(ctx, "SELECT color FROM keeper_color").Scan(&had); err != nil {
				return nil, err
			}
			color := v.(*keeperEntry).Color
			if color == had {
				in.Report(configfile.Item{Action: configfile.Keep})
				return nil, nil
			}
			_, err := tx.ExecContext(ctx, "UPDATE keeper_color SET color = ?", color)
			in.Report(configfile.Item{Action: configfile.Change})
			return nil, err
		},
	})
}

func TestConfigFile(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	var logs syncBuffer
	app, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(&logs, nil)), []module.Module{keeper{}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	m := setupToken.FindStringSubmatch(logs.String())
	if m == nil {
		t.Fatal("no setup token in the log")
	}
	c := &client{t: t, srv: httptest.NewServer(app.Handler())}
	defer c.srv.Close()
	if status, body := c.call(http.MethodPost, "/api/v1/setup", `{"token": "`+m[1]+`", "username": "admin", "password": "correct horse"}`); status != http.StatusCreated {
		t.Fatalf("setup = %d %s", status, body)
	}

	status, file := c.call(http.MethodGet, "/api/v1/config", "")
	if status != http.StatusOK || !strings.HasPrefix(file, "version: 1\n") || !strings.Contains(file, "keeper:\n  color: red\n") {
		t.Fatalf("export = %d\n%s", status, file)
	}
	importFile := func(file string, apply bool) (int, string) {
		body, _ := json.Marshal(map[string]any{"file": file})
		if !apply {
			return c.call(http.MethodPost, "/api/v1/config/check", string(body))
		}
		return c.call(http.MethodPost, "/api/v1/config/import", string(body))
	}
	var rep configfile.Report
	status, body := importFile(strings.Replace(file, "red", "blue", 1), false)
	if err := json.Unmarshal([]byte(body), &rep); status != http.StatusOK || err != nil || rep.Applied || rep.Sections[0].Items[0].Action != configfile.Change {
		t.Errorf("check = %d %s", status, body)
	}
	if _, file := c.call(http.MethodGet, "/api/v1/config", ""); !strings.Contains(file, "color: red") {
		t.Errorf("after the check:\n%s", file)
	}
	status, body = importFile(strings.Replace(file, "red", "blue", 1), true)
	if err := json.Unmarshal([]byte(body), &rep); status != http.StatusOK || err != nil || !rep.Applied || len(rep.Sections) != 1 ||
		rep.Sections[0].Key != "keeper" || rep.Sections[0].Items[0].Action != configfile.Change {
		t.Errorf("import = %d %s", status, body)
	}
	if _, file := c.call(http.MethodGet, "/api/v1/config", ""); !strings.Contains(file, "color: blue") {
		t.Errorf("after the import:\n%s", file)
	}
	if status, body := importFile("version: 9\n", false); status != http.StatusUnprocessableEntity || !strings.Contains(body, `"kernel.config.error.version"`) {
		t.Errorf("a later version = %d %s", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/v1/config/export", `{"password": "wrong"}`); status != http.StatusUnprocessableEntity {
		t.Errorf("export with a wrong password = %d %s", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/v1/config/export", `{"password": "correct horse"}`); status != http.StatusOK || !strings.Contains(body, "keeper:") {
		t.Errorf("export with secrets = %d %s", status, body)
	}
}

package kernel

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/danielgtaylor/huma/v2"

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

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func TestApp(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.BaseURL = "https://example.com/jec"
	log := slog.New(slog.DiscardHandler)

	app, err := New(t.Context(), cfg, log, []module.Module{notes{}})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := get(t, app.Handler(), "/jec/api/v1/notes/count"); status != http.StatusOK || body != "{\"count\":2}\n" {
		t.Errorf("notes/count = %d %s", status, body)
	}
	if err := app.Modules().SetEnabled(t.Context(), "notes", false); err != nil {
		t.Fatal(err)
	}
	if status, _ := get(t, app.Handler(), "/jec/api/v1/notes/count"); status != http.StatusNotFound {
		t.Errorf("switched-off module answered %d", status)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}

	// A restart keeps the data and the module state, and migrates nothing twice.
	app, err = New(t.Context(), cfg, log, []module.Module{notes{}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	status, body := get(t, app.Handler(), "/jec/api/v1/manifest")
	var m struct {
		Modules []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil || status != http.StatusOK || len(m.Modules) != 1 || m.Modules[0].Enabled {
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

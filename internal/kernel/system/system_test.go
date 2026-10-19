package system

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/zone"
)

func TestSystem(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.DB().ExecContext(t.Context(), "CREATE TABLE things (name TEXT); INSERT INTO things VALUES ('kept')"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	tz, err := zone.Open(t.Context(), st.DB(), ny, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	wrong := errors.New("wrong password")
	_, api := humatest.New(t)
	Register(api, Options{
		DataDir: dir, Listen: ":8080", BaseURL: "https://certs.example.com", KeyFromEnv: true, Database: string(st.Dialect()),
		Backup: func(ctx context.Context, path string) error {
			return st.Snapshot(ctx, path, func(ctx context.Context, dst *store.Store) error {
				if _, err := dst.DB().ExecContext(ctx, "CREATE TABLE things (name TEXT)"); err != nil {
					return err
				}
				_, err := dst.Migrate(ctx, "kernel", migrations.FS)
				return err
			})
		},
		Confirm: func(_ context.Context, password string) error {
			if password != "correct horse" {
				return wrong
			}
			return nil
		},
		Now:  func() time.Time { return time.Date(2026, 10, 17, 22, 30, 5, 0, time.UTC) },
		Log:  slog.New(slog.DiscardHandler),
		Zone: tz,
	})

	resp := api.Get("/system")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"dataDir":`) ||
		!strings.Contains(resp.Body.String(), `"listen":":8080","baseURL":"https://certs.example.com","secretKey":"env"`) ||
		!strings.Contains(resp.Body.String(), `"serverTimeZone":"America/New_York"`) || strings.Contains(resp.Body.String(), `"timeZone"`) {
		t.Errorf("GET /system = %d %s", resp.Code, resp.Body.String())
	}

	// The time zone: one chosen, a name that is none, and the server's again.
	resp = api.Put("/system/time-zone", map[string]any{"timeZone": "Asia/Shanghai"})
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"timeZone":"Asia/Shanghai"`) || tz.Location().String() != "Asia/Shanghai" {
		t.Errorf("choosing a zone = %d %s", resp.Code, resp.Body.String())
	}
	resp = api.Put("/system/time-zone", map[string]any{"timeZone": "Mars/Olympus_Mons"})
	if resp.Code != http.StatusUnprocessableEntity || !strings.Contains(resp.Body.String(), `"location":"body.timeZone","code":"kernel.error.unknown_time_zone"`) {
		t.Errorf("an unknown zone = %d %s", resp.Code, resp.Body.String())
	}
	resp = api.Put("/system/time-zone", map[string]any{"timeZone": ""})
	if resp.Code != http.StatusOK || strings.Contains(resp.Body.String(), `"timeZone"`) || tz.Location() != ny {
		t.Errorf("back to the server's = %d %s", resp.Code, resp.Body.String())
	}

	if resp := api.Post("/system/backup", map[string]any{"password": "wrong horse"}); resp.Code == http.StatusOK {
		t.Error("a backup with a wrong password")
	}
	resp = api.Post("/system/backup", map[string]any{"password": "correct horse"})
	if resp.Code != http.StatusOK || resp.Header().Get("Content-Disposition") != `attachment; filename="jec-backup-20261017-223005.db"` ||
		!bytes.HasPrefix(resp.Body.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatalf("backup = %d %v", resp.Code, resp.Header())
	}
	// The copy holds the data, and nothing is left behind.
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restored, resp.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(restored))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err := db.QueryRowContext(t.Context(), "SELECT name FROM things").Scan(&name); err != nil || name != "kept" {
		t.Errorf("the copy holds %q, %v", name, err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "backup-*"))
	if len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

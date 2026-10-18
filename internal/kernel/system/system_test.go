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

	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
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
	wrong := errors.New("wrong password")
	_, api := humatest.New(t)
	Register(api, Options{
		DataDir: dir, Listen: ":8080", BaseURL: "https://certs.example.com", KeyFromEnv: true, DB: st.DB(),
		Confirm: func(_ context.Context, password string) error {
			if password != "correct horse" {
				return wrong
			}
			return nil
		},
		Now: func() time.Time { return time.Date(2026, 10, 17, 22, 30, 5, 0, time.UTC) },
		Log: slog.New(slog.DiscardHandler),
	})

	resp := api.Get("/system")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"dataDir":`) ||
		!strings.Contains(resp.Body.String(), `"listen":":8080","baseURL":"https://certs.example.com","secretKey":"env"`) {
		t.Errorf("GET /system = %d %s", resp.Code, resp.Body.String())
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

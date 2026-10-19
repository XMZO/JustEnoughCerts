package store

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func migration(up string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("-- +goose Up\n" + up + "\n")}
}

func TestMigrate(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	first := fstest.MapFS{
		"00001_items.sql": migration("CREATE TABLE a_items (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT;"),
	}
	got, err := s.Migrate(ctx, "a", first)
	if err != nil || !slices.Equal(got, []int64{1}) {
		t.Fatalf("first run: %v, %v", got, err)
	}
	if got, err := s.Migrate(ctx, "a", first); err != nil || len(got) != 0 {
		t.Fatalf("second run applied %v, %v", got, err)
	}

	// A later version applies on its own; another owner keeps its own history.
	second := fstest.MapFS{
		"00001_items.sql": first["00001_items.sql"],
		"00002_more.sql":  migration("ALTER TABLE a_items ADD COLUMN size INTEGER;"),
	}
	if got, err := s.Migrate(ctx, "a", second); err != nil || !slices.Equal(got, []int64{2}) {
		t.Fatalf("upgrade: %v, %v", got, err)
	}
	other := fstest.MapFS{"00001_b.sql": migration("CREATE TABLE b_things (id INTEGER PRIMARY KEY) STRICT;")}
	if got, err := s.Migrate(ctx, "b", other); err != nil || !slices.Equal(got, []int64{1}) {
		t.Fatalf("other owner: %v, %v", got, err)
	}
	if _, err := s.DB().ExecContext(ctx, "INSERT INTO a_items (name, size) VALUES ('x', 1)"); err != nil {
		t.Fatalf("migrated table: %v", err)
	}
}

func TestMigrateNothing(t *testing.T) {
	s := open(t)
	for name, fsys := range map[string]fstest.MapFS{"nil": nil, "empty": {}} {
		if got, err := s.Migrate(t.Context(), "a", fsys); err != nil || len(got) != 0 {
			t.Errorf("%s: %v, %v", name, got, err)
		}
	}
}

func TestMigrateFailureRollsBack(t *testing.T) {
	s := open(t)
	bad := fstest.MapFS{"00001_bad.sql": migration("CREATE TABLE a_ok (id INTEGER) STRICT;\nCREATE TABLE broken (;")}
	if _, err := s.Migrate(t.Context(), "a", bad); err == nil {
		t.Fatal("a broken migration applied")
	}
	var n int
	if err := s.DB().QueryRowContext(t.Context(), "SELECT count(*) FROM a_ok").Scan(&n); err == nil {
		t.Error("the failed migration left a_ok behind")
	}
}

func TestPragmas(t *testing.T) {
	s := open(t)
	if s.Dialect() != SQLite {
		t.Skip("PRAGMAs are SQLite's")
	}
	for pragma, want := range map[string]string{
		"journal_mode": "wal",
		"foreign_keys": "1",
		"busy_timeout": "10000",
		"synchronous":  "1", // NORMAL
	} {
		var got string
		if err := s.DB().QueryRowContext(t.Context(), "PRAGMA "+pragma).Scan(&got); err != nil || !strings.EqualFold(got, want) {
			t.Errorf("PRAGMA %s = %q, %v; want %q", pragma, got, err, want)
		}
	}
}

func TestDSNEscapesURIChars(t *testing.T) {
	got := dsn("/srv/odd?name#1%/jec.db")
	if !strings.HasPrefix(got, "file:/srv/odd%3Fname%231%25/jec.db?") {
		t.Errorf("dsn = %q", got)
	}
}

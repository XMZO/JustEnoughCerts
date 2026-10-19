package store

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// TestCopy copies a SQLite database into the store of the tests, which is
// PostgreSQL's when they run on it, and back into a new SQLite file.
func TestCopy(t *testing.T) {
	ctx := t.Context()
	schema := fstest.MapFS{"00001_x.sql": migration(`
		CREATE TABLE x_parents (id INTEGER PRIMARY KEY, name TEXT NOT NULL, "user" TEXT NOT NULL, data BLOB) STRICT;
		CREATE TABLE x_children (id INTEGER PRIMARY KEY, parent INTEGER NOT NULL REFERENCES x_parents (id) ON DELETE CASCADE, n INTEGER NOT NULL) STRICT;`)}
	migrate := func(ctx context.Context, s *Store) error {
		_, err := s.Migrate(ctx, "x", schema)
		return err
	}
	src, err := OpenFile(ctx, filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := migrate(ctx, src); err != nil {
		t.Fatal(err)
	}
	// A child before its parent: the copy goes in any order.
	for _, q := range []string{
		`INSERT INTO x_parents (id, name, "user", data) VALUES (7, 'seven', 'admin', x'00ff')`,
		`INSERT INTO x_children (id, parent, n) VALUES (3, 7, 1700000000000)`,
	} {
		if _, err := src.DB().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	dst := open(t)
	if err := migrate(ctx, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.DB().ExecContext(ctx, `INSERT INTO x_parents (name, "user") VALUES ('gone', '')`); err != nil {
		t.Fatal(err)
	}
	if err := Copy(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	var name, user string
	var data []byte
	var n int64
	err = dst.DB().QueryRowContext(ctx, `SELECT p.name, p."user", p.data, c.n FROM x_parents p JOIN x_children c ON c.parent = p.id`).Scan(&name, &user, &data, &n)
	if err != nil || name != "seven" || user != "admin" || string(data) != "\x00\xff" || n != 1700000000000 {
		t.Fatalf("copied %q %q %x %d: %v", name, user, data, n, err)
	}
	// What the copy replaced is gone, and new rows come after its keys.
	var id int64
	if err := dst.DB().QueryRowContext(ctx, `INSERT INTO x_parents (name, "user") VALUES ('new', '') RETURNING id`).Scan(&id); err != nil || id <= 7 {
		t.Errorf("a new row got %d: %v", id, err)
	}
	var count int
	if err := dst.DB().QueryRowContext(ctx, "SELECT count(*) FROM x_parents WHERE name = 'gone'").Scan(&count); err != nil || count != 0 {
		t.Errorf("rows before the copy: %d, %v", count, err)
	}

	// A snapshot of it is a SQLite file again.
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := dst.Snapshot(ctx, path, migrate); err != nil {
		t.Fatal(err)
	}
	back, err := OpenFile(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	if err := back.DB().QueryRowContext(ctx, "SELECT count(*) FROM x_parents").Scan(&count); err != nil || count != 2 {
		t.Errorf("the snapshot holds %d parents: %v", count, err)
	}
}

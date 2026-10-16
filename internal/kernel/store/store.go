// Package store opens the SQLite database in the data directory and applies
// the schema migrations of the kernel and of each module.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	_ "github.com/ncruces/go-sqlite3/driver" // the "sqlite3" driver
	"github.com/pressly/goose/v3"
)

// FileName is the database file in the data directory.
const FileName = "jec.db"

// maxConns bounds the open connections; each holds its own SQLite instance.
const maxConns = 4

// Store is the process's SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens the database in dataDir, creating it if needed.
func Open(ctx context.Context, dataDir string) (*Store, error) {
	db, err := sql.Open("sqlite3", dsn(filepath.Join(dataDir, FileName)))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &Store{db: db}, nil
}

// dsn builds the connection string. WAL lets readers go on while one
// connection writes, immediate transactions make concurrent writers queue on
// the busy timeout instead of failing when a read turns into a write, and
// foreign keys are enforced.
func dsn(path string) string {
	escaped := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(filepath.ToSlash(path))
	return "file:" + escaped +
		"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_txlock=immediate"
}

// DB returns the database handle.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Migrate applies the pending *.sql migrations in fsys, which belong to
// owner: "kernel" or a module ID. Each owner keeps its own version table, so
// modules come and go without touching each other's history. It returns the
// versions it applied; a nil fsys has nothing to apply.
func (s *Store) Migrate(ctx context.Context, owner string, fsys fs.FS) ([]int64, error) {
	if fsys == nil {
		return nil, nil
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.db, fsys,
		goose.WithTableName("migrations_"+owner),
		goose.WithDisableGlobalRegistry(true))
	if errors.Is(err, goose.ErrNoMigrations) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("migrations of %s: %w", owner, err)
	}
	results, err := p.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrations of %s: %w", owner, err)
	}
	versions := make([]int64, 0, len(results))
	for _, r := range results {
		versions = append(versions, r.Source.Version)
	}
	return versions, nil
}

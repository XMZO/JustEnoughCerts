// Package store opens the database, SQLite in the data directory or
// PostgreSQL, and applies the schema migrations of the kernel and of each
// module. The queries and migrations are written for SQLite; PostgreSQL
// takes them rewritten (postgres.go, pgschema.go).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/ncruces/go-sqlite3/driver" // the "sqlite3" driver
	"github.com/pressly/goose/v3"
)

// FileName is the database file in the data directory.
const FileName = "jec.db"

// maxConns bounds the open connections; each holds its own SQLite instance.
const maxConns = 4

// Store is the process's database.
type Store struct {
	db      *sql.DB
	dialect Dialect
}

// Open opens the SQLite database in dataDir, creating it if needed, or a
// schema of the PostgreSQL database of the tests (TestPostgresEnv).
func Open(ctx context.Context, dataDir string) (*Store, error) {
	return OpenURL(ctx, dataDir, "")
}

// OpenURL opens the PostgreSQL database at url, or else as Open does.
func OpenURL(ctx context.Context, dataDir, url string) (*Store, error) {
	switch {
	case url != "":
		db, err := openPostgres(ctx, url, "")
		if err != nil {
			return nil, err
		}
		return &Store{db: db, dialect: Postgres}, nil
	case TestingPostgres():
		return openTestPostgres(ctx, os.Getenv(TestPostgresEnv), dataDir)
	}
	return OpenFile(ctx, filepath.Join(dataDir, FileName))
}

// OpenFile opens the SQLite database at path, creating it if needed.
func OpenFile(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite3", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &Store{db: db, dialect: SQLite}, nil
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

// Dialect tells which database the store holds.
func (s *Store) Dialect() Dialect { return s.dialect }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Migrate applies the pending *.sql migrations in fsys, which belong to
// owner: "kernel" or a module ID. Each owner keeps its own version table, so
// modules come and go without touching each other's history. It returns the
// versions it applied; a nil fsys has nothing to apply.
func (s *Store) Migrate(ctx context.Context, owner string, fsys fs.FS) ([]int64, error) {
	p, err := s.migrations(owner, fsys)
	if p == nil {
		return nil, err
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

// Versions returns the latest version of owner's migrations in fsys that
// the database has, 0 for none, and the latest in fsys.
func (s *Store) Versions(ctx context.Context, owner string, fsys fs.FS) (current, latest int64, err error) {
	p, err := s.migrations(owner, fsys)
	if p == nil {
		return 0, 0, err
	}
	current, latest, err = p.GetVersions(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("migrations of %s: %w", owner, err)
	}
	return current, latest, nil
}

// migrations returns the goose provider of owner's migrations in fsys;
// nil without an error when there are none.
func (s *Store) migrations(owner string, fsys fs.FS) (*goose.Provider, error) {
	if fsys == nil {
		return nil, nil
	}
	dialect := goose.DialectSQLite3
	if s.dialect == Postgres {
		dialect, fsys = goose.DialectPostgres, pgMigrations(fsys)
	} else {
		fsys = sqliteMigrations(fsys)
	}
	p, err := goose.NewProvider(dialect, s.db, fsys,
		goose.WithTableName("migrations_"+owner),
		goose.WithDisableGlobalRegistry(true))
	if errors.Is(err, goose.ErrNoMigrations) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("migrations of %s: %w", owner, err)
	}
	return p, nil
}

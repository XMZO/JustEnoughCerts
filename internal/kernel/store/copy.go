package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Snapshot writes a copy of the database to a new SQLite file at path:
// SQLite's own, through VACUUM INTO; PostgreSQL's, through a file migrated
// by migrate and filled by Copy. Either way the copy is a backup that
// CheckBackup takes.
func (s *Store) Snapshot(ctx context.Context, path string, migrate func(context.Context, *Store) error) error {
	if s.dialect == SQLite {
		_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
		return err
	}
	dst, err := OpenFile(ctx, path)
	if err != nil {
		return err
	}
	if err := migrate(ctx, dst); err != nil {
		return errors.Join(err, dst.Close())
	}
	return errors.Join(Copy(ctx, s, dst), dst.Close())
}

// Copy puts the rows of every table of from in the same table of to, in
// place of what to held, in one transaction of to. Both are migrated to
// the same versions already: the migrations' own tables stay as they are.
func Copy(ctx context.Context, from, to *Store) error {
	tables, err := to.tables(ctx)
	if err != nil {
		return err
	}
	tx, err := to.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// The rows of a table may refer to those of one that comes later.
	deferral := "PRAGMA defer_foreign_keys = ON"
	if to.dialect == Postgres {
		deferral = "SET CONSTRAINTS ALL DEFERRED"
	}
	if _, err := tx.ExecContext(ctx, deferral); err != nil {
		return err
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+quote(table)); err != nil { //nolint:gosec // G202: a table of the schema, quoted
			return fmt.Errorf("copy %s: %w", table, err)
		}
	}
	for _, table := range tables {
		columns, err := to.columns(ctx, table)
		if err != nil {
			return err
		}
		if err := copyTable(ctx, from.db, tx, table, columns); err != nil {
			return fmt.Errorf("copy %s: %w", table, err)
		}
	}
	if to.dialect == Postgres {
		if err := resetIdentities(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func copyTable(ctx context.Context, from *sql.DB, to *sql.Tx, table string, columns []string) error {
	names := make([]string, len(columns))
	for i, c := range columns {
		names[i] = quote(c)
	}
	list := strings.Join(names, ", ")
	rows, err := from.QueryContext(ctx, "SELECT "+list+" FROM "+quote(table)) //nolint:gosec // G202: names of the schema, quoted
	if err != nil {
		return err
	}
	defer rows.Close()
	statement := "INSERT INTO " + quote(table) + " (" + list + ") VALUES (?" + strings.Repeat(", ?", len(columns)-1) + ")" //nolint:gosec // G202: names of the schema, quoted
	insert, err := to.PrepareContext(ctx, statement)
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		if _, err := insert.ExecContext(ctx, values...); err != nil {
			return err
		}
	}
	return rows.Err()
}

// resetIdentities moves the identity of each table of PostgreSQL past the
// keys copied in, for the rows added later.
func resetIdentities(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND is_identity = 'YES'`)
	if err != nil {
		return err
	}
	var keys [][2]string
	for rows.Next() {
		var k [2]string
		if err := rows.Scan(&k[0], &k[1]); err != nil {
			_ = rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, k := range keys {
		_, err := tx.ExecContext(ctx, fmt.Sprintf(
			`SELECT setval(pg_get_serial_sequence('%s', '%s'), coalesce(max(%s), 0) + 1, false) FROM %s`,
			quote(k[0]), k[1], quote(k[1]), quote(k[0])))
		if err != nil {
			return fmt.Errorf("reset the keys of %s: %w", k[0], err)
		}
	}
	return nil
}

// tables lists the tables of the store but for the migrations' own.
func (s *Store) tables(ctx context.Context) ([]string, error) {
	query := `SELECT name FROM sqlite_schema WHERE type = 'table'
		AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT LIKE 'migrations\_%' ESCAPE '\' ORDER BY name`
	if s.dialect == Postgres {
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema()
			AND table_type = 'BASE TABLE' AND table_name NOT LIKE 'migrations\_%' ESCAPE '\' ORDER BY table_name`
	}
	return s.strings(ctx, query)
}

// columns lists the columns of table, in order.
func (s *Store) columns(ctx context.Context, table string) ([]string, error) {
	query := "SELECT name FROM pragma_table_info(?) ORDER BY cid"
	if s.dialect == Postgres {
		query = `SELECT column_name FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? ORDER BY ordinal_position`
	}
	return s.strings(ctx, query, table)
}

func (s *Store) strings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// quote quotes a name for SQL, as both databases take it.
func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// Empty reports whether the store holds no account yet, as a database a
// copy may go into without losing anything.
func (s *Store) Empty(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM kernel_users").Scan(&n)
	return n == 0, err
}

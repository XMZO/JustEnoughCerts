package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Dialect is the kind of database a Store holds.
type Dialect string

// The dialects.
const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

// TestPostgresEnv names a PostgreSQL database for the tests to run on
// instead of SQLite: each data directory gets a schema of its own. It is
// for tests alone.
const TestPostgresEnv = "JEC_TEST_POSTGRES"

// TestingPostgres reports whether the tests run on PostgreSQL.
func TestingPostgres() bool { return os.Getenv(TestPostgresEnv) != "" }

// DialectOf tells which database OpenURL opens for url.
func DialectOf(url string) Dialect {
	if url != "" || TestingPostgres() {
		return Postgres
	}
	return SQLite
}

// openPostgres opens the PostgreSQL database at url, with schema first on
// the search path when it is set. The queries of JEC are written for
// SQLite, which PostgreSQL takes once rewritten (rebind).
func openPostgres(ctx context.Context, url, schema string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		// The error may quote the URL, password and all.
		return nil, errors.New("database URL: not a PostgreSQL connection URL")
	}
	if schema != "" {
		cfg.RuntimeParams["search_path"] = schema
	}
	db := sql.OpenDB(&connector{base: stdlib.GetConnector(*cfg)})
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	return db, nil
}

// openTestPostgres opens the schema of the database at url for a test's
// data directory, creating it the first time: a test that starts JEC
// again on the same directory finds its data again. Each test has a
// directory of its own; the database is a throwaway one.
func openTestPostgres(ctx context.Context, url, dataDir string) (*Store, error) {
	admin, err := openPostgres(ctx, url, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = admin.Close() }()
	sum := sha256.Sum256([]byte(filepath.Clean(dataDir)))
	schema := "t_" + hex.EncodeToString(sum[:8])
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err != nil {
		return nil, fmt.Errorf("test schema: %w", err)
	}
	db, err := openPostgres(ctx, url, schema)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, dialect: Postgres}, nil
}

// connector makes the connections of a PostgreSQL database, which take
// JEC's queries as SQLite does.
type connector struct{ base driver.Connector }

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &pgConn{Conn: conn}, nil
}

func (c *connector) Driver() driver.Driver { return c.base.Driver() }

// pgConn is a connection of pgx's that rewrites each query (rebind), and
// that makes writing transactions take turns, as SQLite's do: a write
// transaction holds a lock of the schema until it ends, so that one that
// reads first, then writes on what it read, is never raced.
type pgConn struct{ driver.Conn }

var (
	_ driver.ExecerContext      = (*pgConn)(nil)
	_ driver.QueryerContext     = (*pgConn)(nil)
	_ driver.ConnPrepareContext = (*pgConn)(nil)
	_ driver.ConnBeginTx        = (*pgConn)(nil)
	_ driver.Pinger             = (*pgConn)(nil)
	_ driver.SessionResetter    = (*pgConn)(nil)
	_ driver.Validator          = (*pgConn)(nil)
	_ driver.NamedValueChecker  = (*pgConn)(nil)
)

func (c *pgConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	q, a, err := rebind(query, args)
	if err != nil {
		return nil, err
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, a)
}

func (c *pgConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, a, err := rebind(query, args)
	if err != nil {
		return nil, err
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, a)
}

func (c *pgConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	q, err := numbered(query)
	if err != nil {
		return nil, err
	}
	stmt, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	return &pgStmt{Stmt: stmt}, nil
}

func (c *pgConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil || opts.ReadOnly {
		return tx, err
	}
	if _, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(current_schema()))", nil); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (c *pgConn) Ping(ctx context.Context) error { return c.Conn.(driver.Pinger).Ping(ctx) }

func (c *pgConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *pgConn) IsValid() bool {
	v, ok := c.Conn.(driver.Validator)
	return !ok || v.IsValid()
}

func (c *pgConn) CheckNamedValue(v *driver.NamedValue) error {
	return c.Conn.(driver.NamedValueChecker).CheckNamedValue(v)
}

// pgStmt is a prepared statement, whose placeholders rebind numbered.
type pgStmt struct{ driver.Stmt }

func (s *pgStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, positional(args))
}

func (s *pgStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, positional(args))
}

func positional(args []driver.NamedValue) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, a := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: a.Value}
	}
	return out
}

// rebind rewrites a query written for SQLite for PostgreSQL: the
// placeholders ? and :name become $1, $2 and so on, in the order of
// args, a name used again keeping its number, and a bool argument 1 or 0;
// LIKE becomes ILIKE, which ignores case as SQLite's LIKE does. Literals,
// quoted names and comments stay as they are, as does a query numbered
// already, such as goose's, whose bools go to columns of booleans.
func rebind(query string, args []driver.NamedValue) (string, []driver.NamedValue, error) {
	q, out, err := rebindCounting(query, args, nil)
	return q, out, err
}

// rebindCounting is rebind; with used set, it tells how many of args the
// placeholders took instead of refusing the rest.
func rebindCounting(query string, args []driver.NamedValue, used *int) (string, []driver.NamedValue, error) {
	var b strings.Builder
	b.Grow(len(query) + 16)
	byName := map[string]driver.NamedValue{}
	var ordered []driver.NamedValue
	for _, a := range args {
		if a.Name != "" {
			byName[a.Name] = a
		} else {
			ordered = append(ordered, a)
		}
	}
	var out []driver.NamedValue
	numbers := map[string]int{}
	next := 0
	bound := false
	add := func(v any) int {
		// SQLite keeps a bool as 1 or 0, in columns of INTEGER.
		if b, ok := v.(bool); ok {
			v = int64(0)
			if b {
				v = int64(1)
			}
		}
		out = append(out, driver.NamedValue{Ordinal: len(out) + 1, Value: v})
		return len(out)
	}
	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch {
		case ch == '\'' || ch == '"':
			j := i + 1
			for j < len(query) {
				if query[j] == ch {
					if j+1 < len(query) && query[j+1] == ch {
						j += 2
						continue
					}
					break
				}
				j++
			}
			end := min(j+1, len(query))
			b.WriteString(query[i:end])
			i = end - 1
		case ch == '-' && strings.HasPrefix(query[i:], "--"):
			j := strings.IndexByte(query[i:], '\n')
			if j < 0 {
				j = len(query) - i
			}
			b.WriteString(query[i : i+j])
			i += j - 1
		case ch == '/' && strings.HasPrefix(query[i:], "/*"):
			j := strings.Index(query[i+2:], "*/")
			end := len(query)
			if j >= 0 {
				end = i + 2 + j + 2
			}
			b.WriteString(query[i:end])
			i = end - 1
		case ch == '?':
			if next >= len(ordered) {
				return "", nil, fmt.Errorf("store: more placeholders than the %d arguments", len(ordered))
			}
			b.WriteString("$" + strconv.Itoa(add(ordered[next].Value)))
			next++
			bound = true
		case ch == ':' && strings.HasPrefix(query[i:], "::"):
			b.WriteString("::")
			i++
		case ch == ':' && i+1 < len(query) && isNameStart(query[i+1]):
			j := i + 1
			for j < len(query) && isNamePart(query[j]) {
				j++
			}
			name := query[i+1 : j]
			a, ok := byName[name]
			if !ok {
				b.WriteString(query[i:j])
				i = j - 1
				continue
			}
			n, seen := numbers[name]
			if !seen {
				n = add(a.Value)
				numbers[name] = n
			}
			b.WriteString("$" + strconv.Itoa(n))
			i = j - 1
			bound = true
		case (ch == 'L' || ch == 'l') && isKeyword(query, i, "LIKE"):
			b.WriteString("ILIKE")
			i += 3
		default:
			b.WriteByte(ch)
		}
	}
	if !bound {
		// Numbered already, as goose's own queries are, or with no
		// arguments at all.
		return b.String(), args, nil
	}
	if used != nil {
		*used = next
	} else if next < len(ordered) {
		return "", nil, fmt.Errorf("store: %d arguments for %d placeholders", len(ordered), next)
	}
	return b.String(), out, nil
}

// numbered is rebind for a statement prepared before its arguments come:
// its placeholders ? are numbered in order; names it cannot take.
func numbered(query string) (string, error) {
	n := strings.Count(query, "?") // at least as many as the placeholders
	args := make([]driver.NamedValue, n)
	for i := range args {
		args[i] = driver.NamedValue{Ordinal: i + 1}
	}
	q, _, err := rebindCounting(query, args, new(int))
	return q, err
}

func isNameStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isNamePart(c byte) bool { return isNameStart(c) || c >= '0' && c <= '9' }

// isKeyword reports whether word, in any case, stands at i of query as a
// word of its own.
func isKeyword(query string, i int, word string) bool {
	if len(query) < i+len(word) || !strings.EqualFold(query[i:i+len(word)], word) {
		return false
	}
	if i > 0 && isNamePart(query[i-1]) {
		return false
	}
	return i+len(word) == len(query) || !isNamePart(query[i+len(word)])
}

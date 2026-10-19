// Package audit is the module of the audit log (PLAN.md 10): what was done
// through the API, by whom and from where, such as sign-ins, changes of
// settings and downloads of private keys and backups, kept for a year.
package audit

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// keep is how long entries stay.
const keep = 365 * 24 * time.Hour

// maxUserAgent bounds the User-Agent kept, which clients make up.
const maxUserAgent = 512

// Module is the audit module.
type Module struct {
	db  *sql.DB
	log *slog.Logger
	now func() time.Time
}

// New returns the module.
func New() *Module { return &Module{now: time.Now} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "audit",
		Category:    module.Feature,
		Name:        i18n.AuditModuleName,
		Description: i18n.AuditModuleDescription,
	}
}

// Migrations returns the module's migrations.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Register records the operations of the API, offers them through its
// own, and prunes them daily.
func (m *Module) Register(r *module.Registry) error {
	m.db, m.log = r.DB, r.Log
	r.Audit(m.record)
	registerAPI(r.API, m)
	return r.Jobs.Add(jobs.Job{Name: "prune", Every: 24 * time.Hour, Timeout: 5 * time.Minute, Run: m.prune})
}

// record stores req. A failure to store it is only logged: the operation
// it tells of is done.
func (m *Module) record(ctx context.Context, req module.Request) {
	agent := req.UserAgent
	if len(agent) > maxUserAgent {
		agent = agent[:maxUserAgent]
	}
	_, err := m.db.ExecContext(context.WithoutCancel(ctx),
		`INSERT INTO audit_log (at, operation, method, path, status, "user", client, user_agent, token) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.At.UnixMilli(), req.Operation, req.Method, req.Path, req.Status, req.User, req.Client, agent, req.Token)
	if err != nil {
		m.log.Error("record an operation", "operation", req.Operation, "err", err)
	}
}

// prune is the job audit.prune: it removes the entries older than keep.
func (m *Module) prune(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, "DELETE FROM audit_log WHERE at < ?", m.now().Add(-keep).UnixMilli())
	return err
}

// entry is an operation of the log.
type entry struct {
	id                                                  int64
	at                                                  time.Time
	operation, method, path, user, client, agent, token string
	status                                              int
}

// query is what a page of the log asks for: the entries since since, of
// result ("ok", "failed", or "" for both), holding text, the newest first,
// limit of them after offset.
type query struct {
	since  time.Time
	result string
	// text is looked for, letter case aside, in the path, the account,
	// the address, the token and the operation's ID; ops are operations
	// whose names hold it, as the interface words them.
	text          string
	ops           []string
	offset, limit int
}

// tally counts the entries of a query's time and text, by result.
type tally struct{ all, ok, failed int }

// find returns the entries of q, and the tally of its time and text.
func (m *Module) find(ctx context.Context, q query) ([]entry, tally, error) {
	where := "at >= ?"
	args := []any{q.since.UnixMilli()}
	if text := strings.ToLower(strings.TrimSpace(q.text)); text != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(text) + "%"
		var holds []string
		for _, column := range []string{"path", `"user"`, "client", "token", "operation"} {
			holds = append(holds, "lower("+column+`) LIKE ? ESCAPE '\'`)
			args = append(args, like)
		}
		if len(q.ops) > 0 {
			holds = append(holds, "operation IN (?"+strings.Repeat(", ?", len(q.ops)-1)+")")
			for _, op := range q.ops {
				args = append(args, op)
			}
		}
		where += " AND (" + strings.Join(holds, " OR ") + ")"
	}
	var t tally
	err := m.db.QueryRowContext(ctx,
		"SELECT count(*), count(CASE WHEN status < 400 THEN 1 END), count(CASE WHEN status >= 400 THEN 1 END) FROM audit_log WHERE "+where, args...).
		Scan(&t.all, &t.ok, &t.failed)
	if err != nil {
		return nil, tally{}, err
	}
	switch q.result {
	case "ok":
		where += " AND status < 400"
	case "failed":
		where += " AND status >= 400"
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, at, operation, method, path, status, "user", client, user_agent, token FROM audit_log
		 WHERE `+where+` ORDER BY at DESC, id DESC LIMIT ? OFFSET ?`, append(args, q.limit, q.offset)...)
	if err != nil {
		return nil, tally{}, err
	}
	defer rows.Close()
	var out []entry
	for rows.Next() {
		var e entry
		var at int64
		if err := rows.Scan(&e.id, &at, &e.operation, &e.method, &e.path, &e.status, &e.user, &e.client, &e.agent, &e.token); err != nil {
			return nil, tally{}, err
		}
		e.at = time.UnixMilli(at).UTC()
		out = append(out, e)
	}
	return out, t, rows.Err()
}

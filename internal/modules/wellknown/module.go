// Package wellknown is the module of domain validations JEC answers itself
// (PLAN.md 5.3.2 ④): a solver over HTTP that keeps the files of the
// validations in its table, and answers the requests for them under
// /.well-known/, which the website's reverse proxy or CDN sends on to JEC.
// It needs no access to the website's files.
package wellknown

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the wellknown module.
type Module struct {
	now func() time.Time
	// web fetches from the public internet, for the test; tests replace it.
	web *http.Client
	db  *sql.DB
}

// New returns the module.
func New() *Module {
	web, _ := httpx.Client(httpx.Options{Timeout: 15 * time.Second, PublicOnly: true})
	return &Module{now: time.Now, web: web}
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "wellknown",
		Category:    module.Solver,
		Name:        i18n.WellknownModuleName,
		Description: i18n.WellknownModuleDescription,
	}
}

// Migrations returns the module's migrations.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Register adds the answer type and the handler of /.well-known/.
func (m *Module) Register(r *module.Registry) error {
	m.db = r.DB
	r.WellKnown(http.HandlerFunc(m.serve))
	return r.Providers.Add(answerType(), m.factory)
}

func answerType() providers.Type {
	return providers.Type{
		ID: "wellknown.answer", Kind: providers.Solver, Challenge: providers.ChallengeHTTP,
		Name: i18n.WellknownAnswerName, Description: i18n.WellknownAnswerDescription,
		Tags: []string{"self_hosted", "http"}, Testable: true,
		Fields: []providers.Field{
			{Key: "domain", Label: i18n.WellknownFieldDomain, Help: i18n.WellknownFieldDomainHelp, Kind: providers.Text},
		},
	}
}

// answer is the solver: the files go to the module's table, whichever
// instance puts them.
type answer struct {
	m      *Module
	domain string // to test with
}

var (
	_ contracts.FileSolver = (*answer)(nil)
	_ contracts.Tester     = (*answer)(nil)
)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	return &answer{m: m, domain: strings.ToLower(strings.TrimSpace(in.Config["domain"]))}, nil
}

// Close has nothing to release.
func (*answer) Close() error { return nil }

// PutFile keeps f to answer with, in place of what the domain had at its
// path.
func (a *answer) PutFile(ctx context.Context, f contracts.File) error {
	if !f.Valid() {
		return retry.Permanent(&contracts.Problem{Key: i18n.WellknownErrorFilePath, Params: map[string]any{"path": f.Path}})
	}
	_, err := a.m.db.ExecContext(ctx,
		`INSERT INTO wellknown_files (domain, path, content, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (domain, path) DO UPDATE SET content = excluded.content, created_at = excluded.created_at`,
		strings.ToLower(f.Domain), f.Path, f.Content, a.m.now().UnixMilli())
	return err
}

// RemoveFile stops answering with f, which may be gone already.
func (a *answer) RemoveFile(ctx context.Context, f contracts.File) error {
	_, err := a.m.db.ExecContext(ctx, "DELETE FROM wellknown_files WHERE domain = ? AND path = ?", strings.ToLower(f.Domain), f.Path)
	return err
}

// Test puts a file for the domain to test with, fetches it from the public
// internet as a CA would, and removes it: it proves that the requests under
// /.well-known/ reach JEC.
func (a *answer) Test(ctx context.Context) (contracts.Message, error) {
	if a.domain == "" {
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.WellknownErrorNoDomain})
	}
	token := make([]byte, 16)
	_, _ = rand.Read(token)
	f := contracts.File{Domain: a.domain, Path: "/.well-known/jec-check/" + hex.EncodeToString(token), Content: []byte(hex.EncodeToString(token))}
	if err := a.PutFile(ctx, f); err != nil {
		return contracts.Message{}, err
	}
	defer func() { _ = a.RemoveFile(context.WithoutCancel(ctx), f) }()
	address := "http://" + a.domain + f.Path
	got, err := a.m.fetch(ctx, address)
	if err == nil && string(got) != string(f.Content) {
		err = fmt.Errorf("got %q", truncate(got))
	}
	if err != nil {
		return contracts.Message{}, &contracts.Problem{Key: i18n.WellknownErrorNotReached, Params: map[string]any{"url": address}, Err: err}
	}
	return contracts.Message{Key: i18n.WellknownTestOk, Params: map[string]any{"url": address}}, nil
}

func (m *Module) fetch(ctx context.Context, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.web.Do(req)
	if err != nil {
		return nil, httpx.Cause(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %q", resp.StatusCode, truncate(data))
	}
	return data, nil
}

func truncate(data []byte) string {
	if len(data) > 200 {
		return string(data[:200]) + "…"
	}
	return string(data)
}

// serve answers a request under /.well-known/ with the file kept for its
// host and path, or else for its path alone, as when the proxy in front
// changed the host. Nothing is cached on the way: the answers change.
func (m *Module) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r) // others may take it
		return
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	var content []byte
	err := m.db.QueryRowContext(r.Context(),
		`SELECT content FROM wellknown_files WHERE path = ?
		 ORDER BY domain = ? DESC, created_at DESC LIMIT 1`, r.URL.Path, strings.ToLower(host)).Scan(&content)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(content)
}

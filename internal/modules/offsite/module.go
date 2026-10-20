// Package offsite is the module that copies the daily backups JEC makes
// (the kernel's, in the backups directory of the data directory) to an
// S3 compatible bucket, as AWS S3, Cloudflare R2, MinIO, or the S3
// interfaces of Alibaba Cloud OSS and Tencent Cloud COS have one, so that
// they outlive the disk they were made on. The backups hold the secrets
// sealed, without the master key.
package offsite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// The backups the kernel makes, in backupDir of the data directory.
const backupDir = "backups"

var backupName = regexp.MustCompile(`^jec-\d{8}-\d{6}\.db$`)

// Module is the offsite module.
type Module struct {
	now     func() time.Time
	client  *http.Client
	db      *sql.DB
	secrets *secrets.Scope
	log     *slog.Logger
	dataDir string
	trigger func(string)
	// call asks the API, for whether daily backups are made at all.
	call func(ctx context.Context, via, method, path string, body, out any) error

	mu      sync.Mutex
	lastErr string // of the last copy, "" when it went well
	lastAt  time.Time
}

// New returns the module.
func New() *Module {
	return &Module{now: time.Now, client: &http.Client{Timeout: 10 * time.Minute}, trigger: func(string) {}}
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "offsite",
		Category:    module.Feature,
		Name:        i18n.OffsiteModuleName,
		Description: i18n.OffsiteModuleDescription,
	}
}

// Migrations returns the tables of the settings and the copies made.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Register adds the API under /api/v1/offsite and the job that copies the
// backups not copied yet.
func (m *Module) Register(r *module.Registry) error {
	m.db, m.secrets, m.log, m.dataDir, m.trigger, m.call = r.DB, r.Secrets, r.Log, r.DataDir, r.Jobs.Trigger, r.Call
	m.register(r.API)
	return r.Jobs.Add(jobs.Job{Name: "copy", Every: 15 * time.Minute, Timeout: 30 * time.Minute, Run: m.copyNew})
}

// copyNew is the job offsite.copy: while switched on, it copies each daily
// backup not copied yet, the oldest first.
func (m *Module) copyNew(ctx context.Context) error {
	s, err := m.load(ctx)
	if err != nil || !s.Enabled {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(m.dataDir, backupDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && backupName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	b := s.bucket(m)
	for _, name := range names {
		var done int
		if err := m.db.QueryRowContext(ctx, "SELECT count(*) FROM offsite_uploads WHERE name = ?", name).Scan(&done); err != nil {
			return err
		}
		if done > 0 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.dataDir, backupDir, name))
		if err != nil {
			return err
		}
		err = b.put(ctx, s.Prefix+name, data)
		m.mu.Lock()
		m.lastAt, m.lastErr = m.now(), ""
		if err != nil {
			m.lastErr = err.Error()
		}
		m.mu.Unlock()
		if err != nil {
			return err
		}
		if _, err := m.db.ExecContext(ctx, "INSERT INTO offsite_uploads (name, size, uploaded_at) VALUES (?, ?, ?)",
			name, len(data), m.now().UnixMilli()); err != nil {
			return err
		}
		m.log.Info("copied a backup", "name", name, "bucket", s.Bucket)
	}
	return nil
}

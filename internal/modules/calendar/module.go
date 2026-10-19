// Package calendar is the module of the calendar feed: the certificates'
// ends and renewals as events, at a secret URL calendar apps subscribe to,
// with a reminder a week before each end. It reads them from JEC's own API.
package calendar

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the calendar module.
type Module struct {
	db      *sql.DB
	call    func(ctx context.Context, via, method, path string, body, out any) error
	zone    func() *time.Location
	baseURL string
	cat     *i18n.Catalog
	now     func() time.Time
}

// New returns the module.
func New() *Module { return &Module{now: time.Now} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "calendar",
		Category:    module.Feature,
		Name:        i18n.CalendarModuleName,
		Description: i18n.CalendarModuleDescription,
	}
}

// Migrations returns the table of the feed's key.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Register adds the API of the feed under /api/v1/calendar.
func (m *Module) Register(r *module.Registry) error {
	cat, err := i18n.Default()
	if err != nil {
		return err
	}
	m.db, m.call, m.zone, m.baseURL, m.cat = r.DB, r.Call, r.Zone, r.BaseURL, cat
	register(r.API, m)
	return nil
}

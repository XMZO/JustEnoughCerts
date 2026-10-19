// Package notify is the module that tells people about what happens to
// their certificates (PLAN.md 4.5, 5.3.4): a note for each event goes
// through each notifier, such as a Telegram bot or a mailbox, that is set
// to want it, written in the notifier's language. The notifiers are
// provider types of other modules (contracts.Notifier). What was sent is
// kept, so that a note is not sent twice when its event comes again, and
// so that the UI can tell how each notifier did last; a notifier can also
// be tested from the UI.
package notify

import (
	"embed"
	"io/fs"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the notify module.
type Module struct {
	now func() time.Time
}

// New returns the module.
func New() *Module { return &Module{now: time.Now} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "notify",
		Category:    module.Feature,
		Name:        i18n.NotifyModuleName,
		Description: i18n.NotifyModuleDescription,
	}
}

// Migrations returns the module's migrations.
func (*Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// noteMaxAge drops notes that would come too late to help.
const noteMaxAge = 3 * 24 * time.Hour

// Register subscribes to the events notes are about, and adds the API
// under /api/v1/notify and the job that forgets old sends.
func (m *Module) Register(r *module.Registry) error {
	cat, err := i18n.Default()
	if err != nil {
		return err
	}
	s := &service{db: r.DB, prov: r.Providers, log: r.Log, cat: cat, base: r.BaseURL, zone: r.Zone, now: m.now}
	types := make([]string, 0, len(contracts.NotifyEvents))
	for _, e := range contracts.NotifyEvents {
		types = append(types, e.Type)
	}
	// A notifier that hangs is cut off after sendTimeout; the others wait
	// their turn within one attempt.
	err = r.Events.Subscribe(events.Subscription{Name: "notes", Types: types, Handler: s.handle, MaxAge: noteMaxAge, Timeout: 5 * time.Minute})
	if err != nil {
		return err
	}
	registerAPI(r.API, s)
	return r.Jobs.Add(jobs.Job{Name: "prune", Every: 24 * time.Hour, Timeout: time.Minute, Run: s.prune})
}

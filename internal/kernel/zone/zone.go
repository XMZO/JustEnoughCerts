// Package zone keeps the time zone chosen for JEC (PLAN.md 11): the panel
// shows times in it, messages tell times in it, and the times of day the
// settings name, such as when renewals start, are in it. Until someone
// chooses one, it is the server's, which the TZ environment variable sets.
package zone

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/configfile"
)

// ErrUnknown is the error of a name that is no time zone this program
// knows.
var ErrUnknown = errors.New("zone: unknown time zone")

// metaName is the name of the chosen zone in kernel_meta.
const metaName = "time_zone"

// Zone is the time zone chosen for JEC.
type Zone struct {
	db     *sql.DB
	server *time.Location

	mu     sync.RWMutex
	chosen *time.Location // nil for the server's
}

// Open reads the zone chosen from db; server is the server's own, as
// Server finds it. A zone stored that this program does not know, as
// after its time zone data changed, gives way to the server's.
func Open(ctx context.Context, db *sql.DB, server *time.Location, log *slog.Logger) (*Zone, error) {
	z := &Zone{db: db, server: server}
	var name []byte
	err := db.QueryRowContext(ctx, "SELECT value FROM kernel_meta WHERE name = ?", metaName).Scan(&name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return z, nil
	case err != nil:
		return nil, err
	}
	loc, err := Load(string(name))
	if err != nil {
		log.Warn("the chosen time zone is unknown; using the server's", "zone", string(name))
		return z, nil
	}
	z.chosen = loc
	return z, nil
}

// Location is the zone times are told in: the one chosen, or the server's.
func (z *Zone) Location() *time.Location {
	z.mu.RLock()
	defer z.mu.RUnlock()
	if z.chosen != nil {
		return z.chosen
	}
	return z.server
}

// Chosen is the zone chosen, nil while it is the server's.
func (z *Zone) Chosen() *time.Location {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.chosen
}

// Server is the server's zone.
func (z *Zone) Server() *time.Location { return z.server }

// Choose makes the zone of an IANA name, such as Asia/Shanghai, the one
// times are told in; "" goes back to the server's. It fails with
// ErrUnknown for a name that is no zone.
func (z *Zone) Choose(ctx context.Context, name string) error {
	loc, err := parse(name)
	if err != nil {
		return err
	}
	if err := write(ctx, z.db, loc); err != nil {
		return err
	}
	z.set(loc)
	return nil
}

// parse returns the zone of a name, nil for "", the server's.
func parse(name string) (*time.Location, error) {
	if name == "" {
		return nil, nil //nolint:nilnil // nil is the server's zone
	}
	return Load(name)
}

// execer is a database or a transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// write stores loc as the zone chosen, nil for the server's.
func write(ctx context.Context, db execer, loc *time.Location) error {
	if loc == nil {
		_, err := db.ExecContext(ctx, "DELETE FROM kernel_meta WHERE name = ?", metaName)
		return err
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO kernel_meta (name, value) VALUES (?, ?)
		 ON CONFLICT (name) DO UPDATE SET value = excluded.value`, metaName, []byte(loc.String()))
	return err
}

func (z *Zone) set(loc *time.Location) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.chosen = loc
}

// Load returns the zone of an IANA name, such as Asia/Shanghai or UTC. It
// does not take "" or Local, which stand for whatever the server has.
func Load(name string) (*time.Location, error) {
	if name == "" || name == "Local" || len(name) > 64 {
		return nil, ErrUnknown
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrUnknown
	}
	return loc, nil
}

// localtime is the file that tells the server's zone when TZ does not.
const localtime = "/etc/localtime"

// Server returns the server's time zone, which the TZ environment variable
// sets. Where Go knows it only as Local, from /etc/localtime, it is named
// after the zone that file links to, if it does, so that the panel can
// show times in it too.
func Server() *time.Location { return server(time.Local, localtime) }

func server(local *time.Location, link string) *time.Location {
	if local.String() != "Local" {
		return local
	}
	target, err := os.Readlink(link)
	if err != nil {
		return local
	}
	_, name, ok := strings.Cut(target, "zoneinfo/")
	if !ok {
		return local
	}
	if loc, err := Load(name); err == nil {
		return loc
	}
	return local
}

// SectionKey is the key of the zone's part of the configuration file.
const SectionKey = "time_zone"

// Section is the zone's part of the configuration file (PLAN.md 11): the
// IANA name of the zone chosen, left out while it is the server's. An
// import of an empty one goes back to the server's.
func (z *Zone) Section() configfile.Section {
	return configfile.Section{
		Key: SectionKey, Title: i18n.KernelConfigSectionTimeZone, New: func() any { return new(string) },
		Export: func(context.Context, configfile.Export) (any, error) {
			loc := z.Chosen()
			if loc == nil {
				return (*string)(nil), nil
			}
			name := loc.String()
			return &name, nil
		},
		Import: z.importSection,
	}
}

func (z *Zone) importSection(ctx context.Context, tx *sql.Tx, v any, in *configfile.Import) (func(), error) {
	loc, err := parse(*v.(*string))
	if err != nil {
		in.Report(configfile.Item{Action: configfile.Skip, Problems: []configfile.Problem{{Key: i18n.KernelErrorUnknownTimeZone}}})
		return nil, nil
	}
	if had := z.Chosen(); (loc == nil) == (had == nil) && (loc == nil || loc.String() == had.String()) {
		in.Report(configfile.Item{Action: configfile.Keep})
		return nil, nil
	}
	if err := write(ctx, tx, loc); err != nil {
		return nil, err
	}
	in.Report(configfile.Item{Action: configfile.Change})
	return func() { z.set(loc) }, nil
}

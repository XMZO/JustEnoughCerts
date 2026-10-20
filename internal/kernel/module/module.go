// Package module defines the modules JEC is built from and tracks which of
// them are switched on.
//
// A module is compiled in by its registration file in internal/modules/all.
// It can be switched off at run time: its API then answers 404, its event
// deliveries wait and its jobs do not run, but its data stays for when it
// is switched back on.
package module

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/configfile"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
)

// Category groups modules on the Modules page.
type Category string

// Categories.
const (
	Source   Category = "source"   // where certificates come from
	Solver   Category = "solver"   // how control of a domain is proven
	Target   Category = "target"   // where certificates are deployed
	Notifier Category = "notifier" // how people hear about events
	Feature  Category = "feature"  // everything else
)

var categories = []Category{Source, Solver, Target, Notifier, Feature}

// Manifest describes a module.
type Manifest struct {
	// ID names the module in URLs, tables and settings, and never changes:
	// lowercase letters, digits and underscores, starting with a letter.
	ID       string
	Category Category
	// Name and Description are shown on the Modules page.
	Name        i18n.Key
	Description i18n.Key
	// Requires lists the modules this one needs; while any of them is off,
	// so is this one.
	Requires []string
	// Required modules are always on.
	Required bool
	// Experimental modules start off, and the UI warns that they may break.
	Experimental bool
}

// Module is a part of JEC that can be compiled out or switched off.
type Module interface {
	Manifest() Manifest
	// Migrations returns the module's *.sql migrations, or nil.
	Migrations() fs.FS
	// Register adds the module's API operations, event subscriptions and
	// jobs. It runs once at startup whether the module is on or not; the
	// kernel keeps what it registers inactive while the module is off.
	Register(r *Registry) error
}

// Registry is what a module registers its parts with.
type Registry struct {
	// API takes the module's operations, under /api/v1/<id>.
	API huma.API
	// DB is the database. A module's tables start with its ID.
	DB *sql.DB
	// Log is the module's logger.
	Log *slog.Logger
	// Events publishes the module's events and subscribes it to events of
	// other modules. Subscribe in Register; publish once JEC runs.
	Events *events.Scope
	// Jobs takes the module's periodic jobs.
	Jobs *jobs.Scope
	// Secrets seals what the module stores but must not keep in the clear,
	// such as private keys and credentials.
	Secrets *secrets.Scope
	// Providers takes the provider types the module offers, such as a DNS
	// provider or a deploy target, and opens configured instances of any
	// module's types.
	Providers *providers.Scope
	// BaseURL is the URL users open JEC at, such as
	// https://certs.example.com, for links in messages; "" when unknown.
	BaseURL string
	// DataDir is the directory JEC keeps its data in, such as /data. What a
	// module keeps there goes in a directory named after its ID.
	DataDir string
	// WellKnown takes the module's handler of the requests under
	// /.well-known/ at the root of the host, whatever JEC's base path: a CA
	// fetching the answer to a domain validation, which the website's
	// proxy or CDN sent on to JEC. They need no sign-in. The handler
	// answers 404 for what is not its own; the next module's handler then
	// gets the request.
	WellKnown func(h http.Handler)
	// ConfirmPassword checks the password of the account a request is
	// signed in as, before something sensitive such as a private key
	// download. Its error is an API response: a wrong body.password, or
	// too many attempts.
	ConfirmPassword func(ctx context.Context, password string) error
	// Audit takes the module's recorder of what is done through the API:
	// after each operation that may change something, any but a GET or a
	// HEAD, it gets what was asked, by whom, from where, and how it ended.
	// Those turned away for want of a session are not recorded. It runs
	// while the module is enabled, before the answer goes out.
	Audit func(record func(context.Context, Request))
	// StartSession signs the account in, for a module that made sure who
	// asks another way than by password, as single sign-on does, and
	// returns the session cookie its answer sets. It fails before the
	// setup made the account.
	StartSession func(ctx context.Context) (http.Cookie, error)
	// Config takes a section the module keeps in the configuration file
	// that people export and import as YAML (PLAN.md 11), such as the
	// certificates. It fails when the section's key is taken.
	Config func(configfile.Section) error
	// Zone returns the time zone chosen for JEC, or the server's: messages
	// tell times in it, and the times of day settings name, such as when
	// renewals start, are in it. It may change while JEC runs, so ask each
	// time.
	Zone func() *time.Location
	// Call calls JEC's own API from inside, for a module that acts for the
	// account another way than a browser does, as a chat bot does: method
	// and path as under /api/v1, with a query if any; body sent as JSON
	// unless nil; the answer decoded into out unless nil. via names who
	// acts, as the audit log shows it. It has the rights of an API token
	// that writes: managing the account is out of its reach. An answer
	// other than a success comes back as *CallError. It works once JEC
	// runs.
	Call func(ctx context.Context, via, method, path string, body, out any) error
}

// CallError is an answer of the API to Registry.Call that is not a
// success: its status, and the error it tells, by message key.
type CallError struct {
	Status int
	Code   i18n.Key
	Params map[string]any
}

func (e *CallError) Error() string { return fmt.Sprintf("api: %d %s", e.Status, e.Code) }

// Request is an operation of the API that may have changed something, as
// Registry.Audit hands it on.
type Request struct {
	At        time.Time
	Operation string // its ID, such as certs-delete
	Method    string
	Path      string // below /api/v1, such as /certs/certificates/12
	Status    int    // of the answer
	User      string // the account signed in; "" for none, as for a sign-in
	Token     string // the name of the API token it came with; "" for a session
	Client    string // the client's address
	UserAgent string
	// Subject is what it acted on, as its handler named it with
	// api.Subject: a certificate's main domain, a provider's name; "" when
	// none did.
	Subject string
}

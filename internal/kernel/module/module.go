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
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
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
}

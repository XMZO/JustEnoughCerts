// Package module defines the modules JEC is built from and tracks which of
// them are switched on.
//
// A module is compiled in by one registration file under cmd/jec. It can be
// switched off at run time: its API then answers 404 and nothing of it runs,
// but its data stays for when it is switched back on.
package module

import (
	"database/sql"
	"io/fs"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
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
	// Register adds the module's API operations and other parts. It runs
	// once at startup whether the module is on or not; the kernel keeps what
	// it registers inactive while the module is off.
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
}

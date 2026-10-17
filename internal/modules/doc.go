// Package modules holds JEC's modules, one directory each (PLAN.md 4.2).
//
// A module talks to the rest of JEC only through the kernel: its Registry,
// the database and, later, the event bus. It never imports another module,
// and the kernel never imports a module, so that deleting a module's
// directory and its file in package all leaves a binary that still builds.
// The test in this package enforces that.
//
// "go run ./tools/new-module -id <id>" scaffolds a new module.
package modules

package module

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

type fake struct{ mf Manifest }

func (f fake) Manifest() Manifest     { return f.mf }
func (fake) Migrations() fs.FS        { return nil }
func (fake) Register(*Registry) error { return nil }
func mod(id string, opts ...func(*Manifest)) Module {
	mf := Manifest{ID: id, Category: Feature, Name: "test.module.name", Description: "test.module.description"}
	for _, o := range opts {
		o(&mf)
	}
	return fake{mf}
}

func requires(ids ...string) func(*Manifest) { return func(m *Manifest) { m.Requires = ids } }
func required(m *Manifest)                   { m.Required = true }
func experimental(m *Manifest)               { m.Experimental = true }

func ids(mods []Module) []string {
	out := make([]string, 0, len(mods))
	for _, m := range mods {
		out = append(out, m.Manifest().ID)
	}
	return out
}

func TestNewSetOrder(t *testing.T) {
	s, err := NewSet(mod("deploy", requires("certs", "dns")), mod("dns"), mod("certs", requires("dns")), mod("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(s.Modules()), []string{"alpha", "dns", "certs", "deploy"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestNewSetErrors(t *testing.T) {
	tests := map[string][]Module{
		"bad id":       {mod("Bad-ID")},
		"kernel id":    {mod("kernel")},
		"long id":      {mod(strings.Repeat("a", 33))},
		"duplicate":    {mod("a"), mod("a")},
		"category":     {mod("a", func(m *Manifest) { m.Category = "other" })},
		"no name":      {mod("a", func(m *Manifest) { m.Name = "" })},
		"unknown req":  {mod("a", requires("b"))},
		"self req":     {mod("a", requires("a"))},
		"cycle":        {mod("a", requires("b")), mod("b", requires("c")), mod("c", requires("a"))},
		"required+exp": {mod("a", required, experimental)},
	}
	for name, mods := range tests {
		if _, err := NewSet(mods...); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func openDB(t *testing.T) (*sql.DB, func() *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	var current *store.Store
	reopen := func() *sql.DB {
		if current != nil {
			_ = current.Close()
		}
		st, err := store.Open(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
			t.Fatal(err)
		}
		current = st
		return st.DB()
	}
	t.Cleanup(func() { _ = current.Close() })
	return reopen(), reopen
}

func load(t *testing.T, db *sql.DB, mods ...Module) *Set {
	t.Helper()
	s, err := NewSet(mods...)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Load(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEnabled(t *testing.T) {
	db, reopen := openDB(t)
	mods := []Module{mod("base"), mod("dep", requires("base")), mod("exp", experimental), mod("core", required)}
	s := load(t, db, mods...)

	// Defaults: on, except experimental modules.
	for id, want := range map[string]bool{"base": true, "dep": true, "exp": false, "core": true, "missing": false} {
		if got := s.Enabled(id); got != want {
			t.Errorf("Enabled(%s) = %v, want %v", id, got, want)
		}
	}

	ctx := t.Context()
	if err := s.SetEnabled(ctx, "base", false); err != nil {
		t.Fatal(err)
	}
	if s.Enabled("dep") {
		t.Error("dep runs while base, which it requires, is off")
	}
	if err := s.SetEnabled(ctx, "exp", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(ctx, "core", false); !errors.Is(err, ErrRequired) {
		t.Errorf("switching off a required module: %v", err)
	}
	if err := s.SetEnabled(ctx, "nope", true); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown module: %v", err)
	}

	// The choices survive a restart; a module compiled out (with the module
	// that requires it) keeps its row.
	db = reopen()
	s = load(t, db, mods[2:]...)
	if err := s.SetEnabled(ctx, "base", true); !errors.Is(err, ErrUnknown) {
		t.Errorf("base is compiled out: %v", err)
	}
	var on bool
	if err := db.QueryRowContext(ctx, "SELECT enabled FROM kernel_modules WHERE id = 'base'").Scan(&on); err != nil || on {
		t.Errorf("stored state of base = %v, %v", on, err)
	}
	if !s.Enabled("exp") {
		t.Error("exp lost its state across the restart")
	}
}

func TestStatuses(t *testing.T) {
	db, _ := openDB(t)
	s := load(t, db,
		mod("zeta", func(m *Manifest) { m.Category = Target }),
		mod("alpha", func(m *Manifest) { m.Category = Target }),
		mod("acme", func(m *Manifest) { m.Category = Source }),
		mod("needs", requires("acme"), func(m *Manifest) { m.Category = Notifier }),
	)
	if err := s.SetEnabled(context.Background(), "acme", false); err != nil {
		t.Fatal(err)
	}
	st := s.Statuses()
	var got []string
	for _, x := range st {
		got = append(got, x.ID)
	}
	if want := []string{"acme", "alpha", "zeta", "needs"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if st[3].Switched != true || st[3].Enabled != false {
		t.Errorf("needs = %+v, want switched on but not running", st[3])
	}
}

func TestSetEnabledBeforeLoad(t *testing.T) {
	s, err := NewSet(mod("a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(t.Context(), "a", false); err == nil {
		t.Error("SetEnabled before Load succeeded")
	}
}

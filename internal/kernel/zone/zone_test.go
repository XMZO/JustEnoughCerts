package zone

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/configfile"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

func open(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := openIn(t)
	return db
}

// openIn returns a database and the directory it is in.
func openIn(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	return st.DB(), dir
}

func TestChoose(t *testing.T) {
	db := open(t)
	log := slog.New(slog.DiscardHandler)
	server := time.FixedZone("server", 3600)
	z, err := Open(t.Context(), db, server, log)
	if err != nil {
		t.Fatal(err)
	}
	if z.Location() != server || z.Chosen() != nil {
		t.Fatalf("before a choice: %v, chosen %v", z.Location(), z.Chosen())
	}

	if err := z.Choose(t.Context(), "Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	if got := z.Location().String(); got != "Asia/Shanghai" || z.Chosen() == nil {
		t.Errorf("after choosing: %s", got)
	}
	// The choice stays, and a later start finds it.
	again, err := Open(t.Context(), db, server, log)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Location().String(); got != "Asia/Shanghai" {
		t.Errorf("after a restart: %s", got)
	}

	for _, name := range []string{"Local", "Mars/Olympus_Mons", "../etc/passwd", "/etc/localtime"} {
		if err := z.Choose(t.Context(), name); !errors.Is(err, ErrUnknown) {
			t.Errorf("Choose(%q) = %v", name, err)
		}
	}
	if got := z.Location().String(); got != "Asia/Shanghai" {
		t.Errorf("a wrong name changed the zone to %s", got)
	}

	if err := z.Choose(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if z.Location() != server || z.Chosen() != nil {
		t.Errorf("back to the server's: %v", z.Location())
	}
	again, err = Open(t.Context(), db, server, log)
	if err != nil {
		t.Fatal(err)
	}
	if again.Location() != server {
		t.Errorf("after going back and a restart: %v", again.Location())
	}
}

func TestOpenUnknown(t *testing.T) {
	db := open(t)
	// As after the time zone data changed: the server's zone instead.
	if _, err := db.ExecContext(t.Context(), "INSERT INTO kernel_meta (name, value) VALUES (?, ?)", metaName, []byte("Gone/Away")); err != nil {
		t.Fatal(err)
	}
	z, err := Open(t.Context(), db, time.UTC, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if z.Location() != time.UTC {
		t.Errorf("Location = %v", z.Location())
	}
}

func TestServer(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if got := server(ny, "/nowhere"); got != ny {
		t.Errorf("a zone TZ named: %v", got)
	}

	local := time.FixedZone("Local", 0)
	dir := t.TempDir()
	link := filepath.Join(dir, "localtime")
	if err := os.Symlink("../usr/share/zoneinfo/Europe/Berlin", link); err != nil {
		t.Fatal(err)
	}
	if got := server(local, link).String(); got != "Europe/Berlin" {
		t.Errorf("from the link: %s", got)
	}
	if got := server(local, filepath.Join(dir, "missing")); got != local {
		t.Errorf("without the file: %v", got)
	}
	odd := filepath.Join(dir, "odd")
	if err := os.Symlink("/somewhere/else", odd); err != nil {
		t.Fatal(err)
	}
	if got := server(local, odd); got != local {
		t.Errorf("a link elsewhere: %v", got)
	}
}

func TestSection(t *testing.T) {
	db, dir := openIn(t)
	log := slog.New(slog.DiscardHandler)
	z, err := Open(t.Context(), db, time.UTC, log)
	if err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	on := func(string) bool { return true }
	reg := providers.New(db, box.Scope("kernel"), on)
	reg.Seal()
	cfg := configfile.New(db, reg, on)
	if err := cfg.Add("kernel", z.Section()); err != nil {
		t.Fatal(err)
	}
	export := func() string {
		t.Helper()
		out, err := cfg.Export(t.Context(), configfile.Export{})
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	file := func(zone string) []byte {
		return fmt.Appendf(nil, "version: %d\ntime_zone: %q\n", configfile.Version, zone)
	}
	result := func(r *configfile.Report) string {
		t.Helper()
		if len(r.Sections) != 1 || r.Sections[0].Key != SectionKey || len(r.Sections[0].Items) != 1 {
			t.Fatalf("report = %+v", r)
		}
		it := r.Sections[0].Items[0]
		if len(it.Problems) > 0 {
			return string(it.Action) + " " + string(it.Problems[0].Key)
		}
		return string(it.Action)
	}

	if out := export(); strings.Contains(out, SectionKey) {
		t.Errorf("the server's zone is written:\n%s", out)
	}
	// A dry run tells, and changes nothing.
	r, err := cfg.Import(t.Context(), file("Asia/Shanghai"), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := result(r); got != "change" || z.Chosen() != nil {
		t.Errorf("dry run: %s, chosen %v", got, z.Chosen())
	}
	if r, err = cfg.Import(t.Context(), file("Asia/Shanghai"), true); err != nil || !r.Applied || z.Location().String() != "Asia/Shanghai" {
		t.Fatalf("import = %+v, %v; zone %v", r, err, z.Location())
	}
	if out := export(); !strings.Contains(out, "time_zone: Asia/Shanghai") {
		t.Errorf("the zone chosen is not written:\n%s", out)
	}
	if r, err = cfg.Import(t.Context(), file("Asia/Shanghai"), true); err != nil || result(r) != "keep" {
		t.Errorf("the same again = %+v, %v", r, err)
	}
	if r, err = cfg.Import(t.Context(), file("Nowhere/Land"), true); err != nil || r.Errors != 1 || result(r) != "skip "+string(i18n.KernelErrorUnknownTimeZone) {
		t.Errorf("an unknown zone = %+v, %v", r, err)
	}
	// An empty one is the server's again, after a restart too.
	if r, err = cfg.Import(t.Context(), file(""), true); err != nil || result(r) != "change" || z.Chosen() != nil {
		t.Errorf("the server's again = %+v, %v; chosen %v", r, err, z.Chosen())
	}
	again, err := Open(t.Context(), db, time.UTC, log)
	if err != nil || again.Chosen() != nil {
		t.Errorf("after a restart: %v, %v", again.Chosen(), err)
	}
}

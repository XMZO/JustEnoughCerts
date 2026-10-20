package kernel

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/system"
)

func TestBackupBeforeMigrating(t *testing.T) {
	if store.TestingPostgres() {
		t.Skip("only a SQLite file is backed up")
	}
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	dir := filepath.Join(cfg.DataDir, system.BackupDir)
	start := func(mods ...module.Module) {
		t.Helper()
		app, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler), mods)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.Close(); err != nil {
			t.Fatal(err)
		}
	}
	backups := func() []string {
		t.Helper()
		names, err := filepath.Glob(filepath.Join(dir, "upgrade-*.db"))
		if err != nil {
			t.Fatal(err)
		}
		return names
	}

	start() // a new database
	start() // nothing to migrate
	if got := backups(); len(got) != 0 {
		t.Fatalf("backups without a migration: %v", got)
	}

	// A module with a migration the database lacks, as a later JEC brings.
	start(notes{})
	got := backups()
	if len(got) != 1 {
		t.Fatalf("backups after a migration: %v", got)
	}
	st, err := store.OpenFile(t.Context(), got[0])
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var tables int
	if err := st.DB().QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name = 'notes_items'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	var users int
	if err := st.DB().QueryRowContext(t.Context(), "SELECT count(*) FROM kernel_users").Scan(&users); err != nil || tables != 0 {
		t.Errorf("the backup is not of the database before: %d notes tables, %v", tables, err)
	}
	if info, err := os.Stat(got[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the backup's mode: %v %v", info.Mode(), err)
	}
	start(notes{})
	if again := backups(); len(again) != 1 {
		t.Errorf("backups after nothing to migrate: %v", again)
	}

	// The latest three stay.
	for _, at := range []string{"20250101-000000", "20250102-000000", "20250103-000000"} {
		if err := os.WriteFile(filepath.Join(dir, "upgrade-"+at+".db"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneUpgrades(dir); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "upgrade-20250102-000000.db"), filepath.Join(dir, "upgrade-20250103-000000.db"), got[0]}
	if left := backups(); !slices.Equal(left, want) {
		t.Errorf("after pruning: %v, want %v", left, want)
	}
}

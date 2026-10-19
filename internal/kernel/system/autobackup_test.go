package system

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

func TestAutoBackup(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	made := 0
	o := Options{
		DataDir: dir, DB: st.DB(), Log: slog.New(slog.DiscardHandler),
		Now: func() time.Time { return now },
		Backup: func(_ context.Context, path string) error {
			made++
			return os.WriteFile(path, []byte("SQLite format 3"), 0o644)
		},
	}
	ctx := t.Context()
	// None until chosen.
	if err := o.autoBackup(ctx); err != nil || made != 0 {
		t.Fatalf("made %d before any were chosen: %v", made, err)
	}
	if err := o.setKeep(ctx, 2); err != nil {
		t.Fatal(err)
	}
	// One a day: the hourly checks between make none.
	for _, step := range []time.Duration{0, time.Hour, 21 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		now = now.Add(step)
		if err := o.autoBackup(ctx); err != nil {
			t.Fatal(err)
		}
	}
	names := o.autoBackups()
	if made != 3 || !slices.Equal(names, []string{"jec-20261023-000000.db", "jec-20261022-000000.db"}) {
		t.Errorf("made %d, kept %v", made, names)
	}
	info, err := os.Stat(filepath.Join(dir, BackupDir, names[0]))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the backup's mode: %v, %v", info.Mode(), err)
	}
	state := o.autoBackupState(ctx)
	if state.Keep != 2 || state.Count != 2 || state.Last == nil || !state.Last.Equal(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("state %+v", state)
	}
	// Fewer to keep: the oldest go at once; none: the files stay.
	if err := o.setKeep(ctx, 1); err != nil || len(o.autoBackups()) != 1 {
		t.Errorf("kept %v: %v", o.autoBackups(), err)
	}
	if err := o.setKeep(ctx, 0); err != nil || len(o.autoBackups()) != 1 {
		t.Errorf("turned off: kept %v: %v", o.autoBackups(), err)
	}
}

package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
)

// backup makes a database as JEC keeps one, with check as its secret
// check value, and returns a copy of it as a backup download makes one.
func backup(t *testing.T, check string) string {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(t.Context(), "INSERT INTO kernel_meta (name, value) VALUES ('secret_check', ?)", []byte(check)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.db")
	if _, err := st.DB().ExecContext(t.Context(), "VACUUM INTO ?", path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckBackup(t *testing.T) {
	path := backup(t, "key")
	verify := func(check []byte) bool { return bytes.Equal(check, []byte("key")) }
	latest := LatestVersion(migrations.FS)
	if latest < 9 {
		t.Fatalf("latest kernel migration %d", latest)
	}
	if err := CheckBackup(t.Context(), path, verify, map[string]int64{"kernel": latest, "later": 3}); err != nil {
		t.Fatalf("a good backup: %v", err)
	}
	if err := CheckBackup(t.Context(), path, func([]byte) bool { return false }, nil); !errors.Is(err, ErrOtherKey) {
		t.Errorf("another key: %v", err)
	}
	if err := CheckBackup(t.Context(), path, verify, map[string]int64{"kernel": latest - 1}); !errors.Is(err, ErrNewer) {
		t.Errorf("a later JEC: %v", err)
	}

	other := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(other, bytes.Repeat([]byte("not a database\n"), 400), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckBackup(t.Context(), other, verify, nil); !errors.Is(err, ErrNotBackup) {
		t.Errorf("a text file: %v", err)
	}
	// Some SQLite database, but not JEC's.
	plain, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	foreign := filepath.Join(t.TempDir(), "other.db")
	if _, err := plain.DB().ExecContext(t.Context(), "CREATE TABLE things (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.DB().ExecContext(t.Context(), "VACUUM INTO ?", foreign); err != nil {
		t.Fatal(err)
	}
	if err := CheckBackup(t.Context(), foreign, verify, nil); !errors.Is(err, ErrNotBackup) {
		t.Errorf("another database: %v", err)
	}
	// Cut short, as an upload that broke off.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cut := filepath.Join(t.TempDir(), "cut.db")
	if err := os.WriteFile(cut, data[:len(data)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckBackup(t.Context(), cut, verify, nil); err == nil {
		t.Error("a backup cut in half passed")
	}
}

func TestApplyRestore(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 18, 12, 30, 0, 0, time.UTC)
	if kept, err := ApplyRestore(dir, now); err != nil || kept != "" {
		t.Fatalf("nothing waiting: %q %v", kept, err)
	}
	for name, content := range map[string]string{FileName: "current", FileName + "-wal": "log", RestoreFile: "backup"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := ApplyRestore(dir, now)
	if err != nil || kept != "jec.db.before-restore-20261018-123000" {
		t.Fatalf("kept %q: %v", kept, err)
	}
	for name, want := range map[string]string{FileName: "backup", kept: "current", kept + "-wal": "log"} {
		if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(got) != want {
			t.Errorf("%s = %q %v, want %q", name, got, err, want)
		}
	}
	for _, gone := range []string{RestoreFile, FileName + "-wal"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s left: %v", gone, err)
		}
	}
}

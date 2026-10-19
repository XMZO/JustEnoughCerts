package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RestoreFile is a backup waiting in the data directory to take the place
// of the database at the next start (ApplyRestore).
const RestoreFile = "restore.db"

// Why CheckBackup refuses a file.
var (
	ErrNotBackup = errors.New("store: not a database of JEC")
	ErrDamaged   = errors.New("store: the database is damaged")
	ErrOtherKey  = errors.New("store: the database was sealed with another secret key")
	ErrNewer     = errors.New("store: the database comes from a later JEC")
)

// ApplyRestore puts the backup waiting in dataDir in the place of the
// database, before it is opened, and keeps the database it replaces, with
// its write-ahead log, beside it under a name of now. It returns that name,
// or "" when no backup was waiting.
func ApplyRestore(dataDir string, now time.Time) (string, error) {
	pending := filepath.Join(dataDir, RestoreFile)
	if _, err := os.Stat(pending); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	db := filepath.Join(dataDir, FileName)
	kept := FileName + ".before-restore-" + now.UTC().Format("20060102-150405")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Rename(db+suffix, filepath.Join(dataDir, kept+suffix)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("keep the database before the restore: %w", err)
		}
	}
	if err := os.Rename(pending, db); err != nil {
		return "", fmt.Errorf("restore the backup: %w", err)
	}
	return kept, nil
}

// ApplyRestoreTo takes the backup waiting in dataDir into st, whose
// database is no file to swap, as PostgreSQL's: the backup, migrated as st
// is by migrate, takes the place of its rows. A copy of what st held is
// kept in dataDir first, as a SQLite file under a name of now, which it
// returns; "" when no backup was waiting.
func ApplyRestoreTo(ctx context.Context, st *Store, dataDir string, now time.Time, migrate func(context.Context, *Store) error) (string, error) {
	pending := filepath.Join(dataDir, RestoreFile)
	if _, err := os.Stat(pending); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	kept := FileName + ".before-restore-" + now.UTC().Format("20060102-150405")
	if err := st.Snapshot(ctx, filepath.Join(dataDir, kept), migrate); err != nil {
		return "", fmt.Errorf("keep the database before the restore: %w", err)
	}
	src, err := OpenFile(ctx, pending)
	if err != nil {
		return "", fmt.Errorf("restore the backup: %w", err)
	}
	err = migrate(ctx, src)
	if err == nil {
		err = Copy(ctx, src, st)
	}
	if err = errors.Join(err, src.Close()); err != nil {
		return "", fmt.Errorf("restore the backup: %w", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(pending + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	return kept, nil
}

// CheckBackup tells whether the file at path is a database of JEC that
// this program can take in place of its own: sound, sealed with the key
// verify accepts the check value of, and with no migration of an owner
// later than the last of versions, those this program has. Owners the
// program lacks, as modules left out of the build, do not count.
func CheckBackup(ctx context.Context, path string, verify func(check []byte) bool, versions map[string]int64) error {
	escaped := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(filepath.ToSlash(path))
	// The file is JEC's own copy, which nothing changes: immutable spares
	// the locks and the shared memory a write-ahead log needs.
	db, err := sql.Open("sqlite3", "file:"+escaped+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		if strings.Contains(err.Error(), "not a database") {
			return ErrNotBackup
		}
		return fmt.Errorf("%w: %w", ErrDamaged, err)
	}
	if result != "ok" {
		return fmt.Errorf("%w: %s", ErrDamaged, result)
	}
	var check []byte
	err = db.QueryRowContext(ctx, "SELECT value FROM kernel_meta WHERE name = 'secret_check'").Scan(&check)
	switch {
	case err != nil:
		return fmt.Errorf("%w: %w", ErrNotBackup, err)
	case !verify(check):
		return ErrOtherKey
	}
	for owner, latest := range versions {
		var v sql.NullInt64
		err := db.QueryRowContext(ctx, `SELECT max(version_id) FROM "migrations_`+owner+`"`).Scan(&v)
		switch {
		case err != nil && strings.Contains(err.Error(), "no such table"):
			continue // the owner came after the backup
		case err != nil:
			return fmt.Errorf("%w: %w", ErrDamaged, err)
		case v.Int64 > latest:
			return fmt.Errorf("%w: %s is at %d, this program at %d", ErrNewer, owner, v.Int64, latest)
		}
	}
	return nil
}

var migrationName = regexp.MustCompile(`^(\d+)_.*\.sql$`)

// LatestVersion returns the version of the last migration in fsys, 0 for
// none.
func LatestVersion(fsys fs.FS) int64 {
	var latest int64
	if fsys == nil {
		return 0
	}
	entries, _ := fs.ReadDir(fsys, ".")
	for _, e := range entries {
		if m := migrationName.FindStringSubmatch(e.Name()); m != nil {
			if v, err := strconv.ParseInt(m[1], 10, 64); err == nil && v > latest {
				latest = v
			}
		}
	}
	return latest
}

package kernel

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/system"
)

// keepUpgrades is how many of the backups made before migrations are kept.
const keepUpgrades = 3

var upgradeName = regexp.MustCompile(`^upgrade-\d{8}-\d{6}\.db$`)

// backupBeforeMigrating copies the SQLite database of st into the backups
// of dataDir when a later JEC is about to migrate it, so that the earlier
// JEC can take it back, and keeps the latest keepUpgrades such copies. A
// new database, or one with nothing to migrate, needs none; a PostgreSQL
// one is backed up by its own tools.
func backupBeforeMigrating(ctx context.Context, st *store.Store, dataDir string, mods []module.Module, now time.Time) (string, error) {
	if st.Dialect() != store.SQLite {
		return "", nil
	}
	if current, _, err := st.Versions(ctx, owner, migrations.FS); err != nil || current == 0 {
		return "", err
	}
	behind, err := migrationsDue(ctx, st, mods)
	if err != nil || !behind {
		return "", err
	}
	dir := filepath.Join(dataDir, system.BackupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "upgrade-" + now.UTC().Format("20060102-150405") + ".db"
	part := filepath.Join(dir, name+".part")
	_ = os.Remove(part)
	if err := st.Snapshot(ctx, part, nil); err != nil {
		_ = os.Remove(part)
		return "", err
	}
	if err := os.Chmod(part, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(part, filepath.Join(dir, name)); err != nil {
		return "", err
	}
	return filepath.Join(system.BackupDir, name), pruneUpgrades(dir)
}

// migrationsDue reports whether the kernel or a module of mods has
// migrations the database lacks.
func migrationsDue(ctx context.Context, st *store.Store, mods []module.Module) (bool, error) {
	current, latest, err := st.Versions(ctx, owner, migrations.FS)
	if err != nil || current < latest {
		return err == nil, err
	}
	for _, m := range mods {
		current, latest, err := st.Versions(ctx, m.Manifest().ID, m.Migrations())
		if err != nil || current < latest {
			return err == nil, err
		}
	}
	return false, nil
}

func pruneUpgrades(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && upgradeName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	for _, n := range names[:max(len(names)-keepUpgrades, 0)] {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			return fmt.Errorf("remove %s: %w", n, err)
		}
	}
	return nil
}

// logUpgradeBackup tells of the backup made before migrating, or of why
// there is none: JEC goes on either way, as it did before it made them.
func logUpgradeBackup(log *slog.Logger, file string, err error) {
	switch {
	case err != nil:
		log.Warn("could not back the database up before migrating it", "err", err)
	case file != "":
		log.Info("backed the database up before migrating it", "file", file)
	}
}

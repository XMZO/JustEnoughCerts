package system

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
)

// The backups JEC makes by itself: one a day, in BackupDir of the data
// directory, of which it keeps the latest, as many as chosen (none until
// then). They are as a downloaded backup is: without the master key.
const (
	BackupDir      = "backups"
	autoBackupMeta = "auto_backup"
	maxKeep        = 30
	backupEvery    = 23 * time.Hour // a little under a day, for the hourly checks
)

var backupName = regexp.MustCompile(`^jec-\d{8}-\d{6}\.db$`)

// AutoBackupBody is the choice of backups JEC makes by itself, and those
// it made.
type AutoBackupBody struct {
	Keep  int        `json:"keep" minimum:"0" maximum:"30" doc:"How many daily backups to keep in the data directory; 0 makes none"`
	Count int        `json:"count" doc:"The backups made there that are kept"`
	Last  *time.Time `json:"last,omitempty" doc:"When the latest one was made"`
}

type autoBackupInput struct {
	Body struct {
		Keep int `json:"keep" minimum:"0" maximum:"30"`
	}
}

// registerAutoBackup adds PUT /system/auto-backup, and the hourly job that
// makes a backup when the latest is a day old.
func registerAutoBackup(a huma.API, o Options, tags []string) {
	huma.Register(a, huma.Operation{
		OperationID: "set-auto-backup", Method: http.MethodPut, Path: "/system/auto-backup", Tags: tags,
		Summary: "Choose how many daily backups JEC makes and keeps in its data directory; 0 for none",
	}, func(ctx context.Context, in *autoBackupInput) (*struct{ Body RuntimeBody }, error) {
		if err := o.setKeep(ctx, in.Body.Keep); err != nil {
			return nil, err
		}
		if in.Body.Keep > 0 && o.Jobs != nil {
			o.Jobs.Trigger("backup") // the first one now, if there is none of today
		}
		return &struct{ Body RuntimeBody }{Body: o.runtime(ctx)}, nil
	})
	if o.Jobs != nil {
		// The error, as a full disk, shows in the log; the next hour tries again.
		_ = o.Jobs.Add(jobs.Job{Name: "backup", Every: time.Hour, Timeout: 30 * time.Minute, Run: o.autoBackup})
	}
}

// keep is how many daily backups to keep, 0 for none.
func (o Options) keep(ctx context.Context) (int, error) {
	if o.DB == nil {
		return 0, nil
	}
	var v []byte
	err := o.DB.QueryRowContext(ctx, "SELECT value FROM kernel_meta WHERE name = ?", autoBackupMeta).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(v))
}

func (o Options) setKeep(ctx context.Context, n int) error {
	_, err := o.DB.ExecContext(ctx,
		`INSERT INTO kernel_meta (name, value) VALUES (?, ?) ON CONFLICT (name) DO UPDATE SET value = excluded.value`,
		autoBackupMeta, []byte(strconv.Itoa(n)))
	if err != nil || n == 0 {
		return err // turned off, JEC leaves the backups made
	}
	return o.prune(n)
}

// autoBackups lists the daily backups, the latest first.
func (o Options) autoBackups() []string {
	entries, err := os.ReadDir(filepath.Join(o.DataDir, BackupDir))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && backupName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	slices.Reverse(names)
	return names
}

// autoBackupState tells what GET /system shows of the daily backups.
func (o Options) autoBackupState(ctx context.Context) AutoBackupBody {
	keep, err := o.keep(ctx)
	if err != nil {
		o.Log.Warn("cannot read the choice of daily backups", "err", err)
	}
	names := o.autoBackups()
	body := AutoBackupBody{Keep: keep, Count: len(names)}
	if len(names) > 0 {
		if t, err := time.Parse("20060102-150405", names[0][len("jec-"):len(names[0])-len(".db")]); err == nil {
			body.Last = &t
		}
	}
	return body
}

// autoBackup makes a backup once the latest is a day old, while some are
// to be kept, and lets the oldest go.
func (o Options) autoBackup(ctx context.Context) error {
	keep, err := o.keep(ctx)
	if err != nil || keep == 0 {
		return err
	}
	now := o.Now().UTC()
	if names := o.autoBackups(); len(names) > 0 {
		last, err := time.Parse("20060102-150405", names[0][len("jec-"):len(names[0])-len(".db")])
		if err == nil && now.Sub(last) < backupEvery {
			return o.prune(keep)
		}
	}
	dir := filepath.Join(o.DataDir, BackupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("daily backup: %w", err)
	}
	name := "jec-" + now.Format("20060102-150405") + ".db"
	part := filepath.Join(dir, name+".part")
	_ = os.Remove(part)
	if err := o.Backup(ctx, part); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("daily backup: %w", err)
	}
	if err := os.Chmod(part, 0o600); err != nil {
		return fmt.Errorf("daily backup: %w", err)
	}
	if err := os.Rename(part, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("daily backup: %w", err)
	}
	o.Log.Info("daily backup made", "file", filepath.Join(BackupDir, name))
	return o.prune(keep)
}

// prune keeps the latest keep daily backups and removes the others.
func (o Options) prune(keep int) error {
	names := o.autoBackups()
	if len(names) <= keep {
		return nil
	}
	var errs []error
	for _, n := range names[keep:] {
		if err := os.Remove(filepath.Join(o.DataDir, BackupDir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

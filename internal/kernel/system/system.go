// Package system tells how JEC runs, for the settings page, and makes
// backups of its database (PLAN.md 6.1 and 10).
package system

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/zone"
)

// Options are what the operations tell and use.
type Options struct {
	DataDir, Listen, BaseURL string
	// TrustedProxies and HSTS are those settings, as config has them.
	TrustedProxies string
	HSTS           bool
	// VerifyKey tells whether a database's check value is the master
	// key's; Versions are the last migration of each owner in this
	// program. A backup is restored only when both agree with it.
	VerifyKey func(check []byte) bool
	Versions  map[string]int64
	// Restart stops JEC for what started it to start it again, which then
	// takes in the restored backup; nil leaves JEC running.
	Restart func()
	// KeyFromEnv tells that the master key comes from JEC_SECRET_KEY, not
	// from the key file in DataDir.
	KeyFromEnv bool
	// Database is the kind of database JEC keeps its data in, sqlite or
	// postgres; Backup writes a copy of it to a new SQLite file at path.
	Database string
	Backup   func(ctx context.Context, path string) error
	// Confirm checks the password of the signed-in account, as
	// auth.Service.ConfirmPassword does.
	Confirm func(ctx context.Context, password string) error
	Now     func() time.Time
	Log     *slog.Logger
	// Version is the running one, which a check for releases compares;
	// Client asks GitHub for them.
	Version string
	Client  *http.Client
	// Zone is the time zone chosen for JEC, which the settings change.
	Zone *zone.Zone
}

// RuntimeBody is how this JEC runs: where its data is and how it is reached.
type RuntimeBody struct {
	DataDir   string `json:"dataDir" doc:"Where the database and the key file are"`
	Listen    string `json:"listen" doc:"The address the HTTP server binds to, from JEC_LISTEN"`
	BaseURL   string `json:"baseURL,omitempty" doc:"The URL people open, from JEC_BASE_URL; links in notifications need it"`
	SecretKey string `json:"secretKey" enum:"env,file" doc:"Where the master key comes from: JEC_SECRET_KEY, or the key file in dataDir"`
	Database  string `json:"database" enum:"sqlite,postgres" doc:"Where the data is: SQLite in dataDir, or the PostgreSQL that JEC_DATABASE_URL names"`
	// The proxies are addresses and ranges, data to show as they are.
	TrustedProxies string `json:"trustedProxies,omitempty" doc:"The reverse proxies whose X-Forwarded-For JEC believes, from JEC_TRUSTED_PROXIES"`
	HSTS           bool   `json:"hsts" doc:"Answers send Strict-Transport-Security, as JEC_HSTS asks"`
	TimeZone       string `json:"timeZone,omitempty" doc:"The time zone chosen for JEC, an IANA name: the panel shows times in it, messages tell times in it, and renewals start at the check time in it; empty while it is the server's"`
	ServerTimeZone string `json:"serverTimeZone" doc:"The server's time zone, which TZ sets: an IANA name, or Local when the server tells none"`
}

type timeZoneInput struct {
	Body struct {
		TimeZone string `json:"timeZone" maxLength:"64" doc:"An IANA name, such as Asia/Shanghai or UTC; empty for the server's"`
	}
}

type backupInput struct {
	Body struct {
		Password string `json:"password" maxLength:"1024" doc:"The password of the signed-in account"`
	}
}

type backupOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	CacheControl       string `header:"Cache-Control"`
	Body               []byte
}

// Register adds GET /system, PUT /system/time-zone, POST /system/backup,
// POST /system/restore and GET /system/release to a.
func Register(a huma.API, o Options) {
	tags := []string{"system"}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	registerRelease(a, client, o.Version, tags)
	huma.Register(a, huma.Operation{
		OperationID: "get-system", Method: http.MethodGet, Path: "/system", Tags: tags,
		Summary: "Tell where JEC keeps its data and how it is reached",
	}, func(context.Context, *struct{}) (*struct{ Body RuntimeBody }, error) {
		return &struct{ Body RuntimeBody }{Body: o.runtime()}, nil
	})
	huma.Register(a, huma.Operation{
		OperationID: "set-time-zone", Method: http.MethodPut, Path: "/system/time-zone", Tags: tags,
		Summary: "Choose the time zone JEC tells times in, or go back to the server's",
	}, func(ctx context.Context, in *timeZoneInput) (*struct{ Body RuntimeBody }, error) {
		err := o.Zone.Choose(ctx, in.Body.TimeZone)
		if errors.Is(err, zone.ErrUnknown) {
			return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil).
				WithField("body.timeZone", i18n.KernelErrorUnknownTimeZone)
		}
		if err != nil {
			return nil, err
		}
		return &struct{ Body RuntimeBody }{Body: o.runtime()}, nil
	})

	registerRestore(a, o, tags)
	huma.Register(a, huma.Operation{
		OperationID: "backup", Method: http.MethodPost, Path: "/system/backup", Tags: tags,
		Summary: "Download a copy of the database, once the password is confirmed",
		Description: "The copy holds the certificates, their private keys and the credentials, sealed with the master key, " +
			"which is not in it. To restore it, stop JEC and put the file in the data directory as " + store.FileName + ".",
		Responses: map[string]*huma.Response{"200": {
			Description: "The SQLite database",
			Content:     map[string]*huma.MediaType{"application/vnd.sqlite3": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}},
		}},
	}, func(ctx context.Context, in *backupInput) (*backupOutput, error) {
		if err := o.Confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		data, err := o.backup(ctx)
		if err != nil {
			return nil, err
		}
		u, _ := auth.UserFrom(ctx)
		o.Log.Info("backup downloaded", "username", u.Username, "bytes", len(data))
		return &backupOutput{
			ContentType:        "application/vnd.sqlite3",
			ContentDisposition: `attachment; filename="jec-backup-` + o.Now().Format("20060102-150405") + `.db"`,
			CacheControl:       "no-store",
			Body:               data,
		}, nil
	})
}

// runtime is what GET /system tells.
func (o Options) runtime() RuntimeBody {
	info := RuntimeBody{
		DataDir: o.DataDir, Listen: o.Listen, BaseURL: o.BaseURL, SecretKey: "file", TrustedProxies: o.TrustedProxies, HSTS: o.HSTS,
		Database:       o.Database,
		ServerTimeZone: o.Zone.Server().String(),
	}
	if o.KeyFromEnv {
		info.SecretKey = "env"
	}
	if loc := o.Zone.Chosen(); loc != nil {
		info.TimeZone = loc.String()
	}
	return info
}

// maxRestore bounds the backup a restore takes.
const maxRestore = 1 << 30

type restoreForm struct {
	File     huma.FormFile `form:"file" required:"true" doc:"A backup JEC made"`
	Password string        `form:"password" doc:"The password of the signed-in account"`
}

type restoreInput struct {
	RawBody huma.MultipartFormFiles[restoreForm]
}

// registerRestore adds POST /system/restore to a.
func registerRestore(a huma.API, o Options, tags []string) {
	huma.Register(a, huma.Operation{
		OperationID: "restore", Method: http.MethodPost, Path: "/system/restore", Tags: tags,
		Metadata: map[string]any{auth.SessionOnly: true}, DefaultStatus: http.StatusAccepted,
		MaxBodyBytes: maxRestore, BodyReadTimeout: 10 * time.Minute,
		Summary: "Restore a backup, once the password is confirmed: JEC stops, to start again with it",
		Description: "The backup has to be sound, sealed with the master key JEC has now, and of a JEC no later than this one. " +
			"It waits in the data directory as " + store.RestoreFile + " until JEC starts again, which then keeps its database " +
			"as it was beside it. Whatever started JEC, such as Docker, has to start it again.",
	}, func(ctx context.Context, in *restoreInput) (*struct{}, error) {
		form := in.RawBody.Data()
		if err := o.Confirm(ctx, form.Password); err != nil {
			return nil, err
		}
		if err := o.stage(ctx, form.File); err != nil {
			return nil, err
		}
		u, _ := auth.UserFrom(ctx)
		o.Log.Warn("a backup waits to be restored: JEC stops, to start again with it", "username", u.Username)
		if o.Restart != nil {
			// Once this answer is out.
			time.AfterFunc(time.Second, o.Restart)
		}
		return nil, nil
	})
}

// stage checks the backup and leaves it in the data directory for the
// next start.
func (o Options) stage(ctx context.Context, file huma.FormFile) error {
	upload := filepath.Join(o.DataDir, store.RestoreFile+".upload")
	defer func() { _ = os.Remove(upload) }()
	out, err := os.OpenFile(upload, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	_, err = io.Copy(out, file)
	if err = errors.Join(err, out.Close()); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	err = store.CheckBackup(ctx, upload, o.VerifyKey, o.Versions)
	var key i18n.Key
	switch {
	case errors.Is(err, store.ErrNotBackup):
		key = i18n.KernelErrorRestoreNotBackup
	case errors.Is(err, store.ErrOtherKey):
		key = i18n.KernelErrorRestoreOtherKey
	case errors.Is(err, store.ErrNewer):
		key = i18n.KernelErrorRestoreNewer
	case err != nil:
		o.Log.Warn("refused a backup to restore", "err", err)
		key = i18n.KernelErrorRestoreDamaged
	}
	if key != "" {
		return api.NewError(http.StatusUnprocessableEntity, key, nil)
	}
	return os.Rename(upload, filepath.Join(o.DataDir, store.RestoreFile))
}

// backup copies the database as it is now, consistent while JEC goes on,
// into a SQLite file (store.Store.Snapshot). The copy goes to the data
// directory, which is the one place JEC may write to (the image has no
// /tmp).
func (o Options) backup(ctx context.Context) ([]byte, error) {
	dir, err := os.MkdirTemp(o.DataDir, "backup-")
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, store.FileName)
	if err := o.Backup(ctx, path); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	return os.ReadFile(path)
}

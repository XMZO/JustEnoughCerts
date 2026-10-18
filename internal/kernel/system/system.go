// Package system tells how JEC runs, for the settings page, and makes
// backups of its database (PLAN.md 6.1 and 10).
package system

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

// Options are what the operations tell and use.
type Options struct {
	DataDir, Listen, BaseURL string
	// KeyFromEnv tells that the master key comes from JEC_SECRET_KEY, not
	// from the key file in DataDir.
	KeyFromEnv bool
	DB         *sql.DB
	// Confirm checks the password of the signed-in account, as
	// auth.Service.ConfirmPassword does.
	Confirm func(ctx context.Context, password string) error
	Now     func() time.Time
	Log     *slog.Logger
}

// RuntimeBody is how this JEC runs: where its data is and how it is reached.
type RuntimeBody struct {
	DataDir   string `json:"dataDir" doc:"Where the database and the key file are"`
	Listen    string `json:"listen" doc:"The address the HTTP server binds to, from JEC_LISTEN"`
	BaseURL   string `json:"baseURL,omitempty" doc:"The URL people open, from JEC_BASE_URL; links in notifications need it"`
	SecretKey string `json:"secretKey" enum:"env,file" doc:"Where the master key comes from: JEC_SECRET_KEY, or the key file in dataDir"`
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

// Register adds GET /system and POST /system/backup to a.
func Register(a huma.API, o Options) {
	tags := []string{"system"}
	huma.Register(a, huma.Operation{
		OperationID: "get-system", Method: http.MethodGet, Path: "/system", Tags: tags,
		Summary: "Tell where JEC keeps its data and how it is reached",
	}, func(context.Context, *struct{}) (*struct{ Body RuntimeBody }, error) {
		info := RuntimeBody{DataDir: o.DataDir, Listen: o.Listen, BaseURL: o.BaseURL, SecretKey: "file"}
		if o.KeyFromEnv {
			info.SecretKey = "env"
		}
		return &struct{ Body RuntimeBody }{Body: info}, nil
	})

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

// backup copies the database as it is now: VACUUM INTO writes a
// consistent copy while JEC goes on. The copy goes to the data directory,
// which is the one place JEC may write to (the image has no /tmp).
func (o Options) backup(ctx context.Context) ([]byte, error) {
	dir, err := os.MkdirTemp(o.DataDir, "backup-")
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, store.FileName)
	if _, err := o.DB.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	return os.ReadFile(path)
}

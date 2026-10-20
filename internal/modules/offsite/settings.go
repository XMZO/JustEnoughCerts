package offsite

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
)

// settings are those of the bucket, the secret key opened.
type settings struct {
	Enabled   bool
	Kind      string // kindS3 or kindWebDAV
	Endpoint  string
	Region    string
	Bucket    string
	Prefix    string
	PathStyle bool
	AccessKey string
	SecretKey string
}

const secretContext = "offsite secret key"

// Where backups go.
const (
	kindS3     = "s3"
	kindWebDAV = "webdav"
)

// destination is where backups go: a bucket or a WebDAV folder.
type destination interface {
	put(ctx context.Context, key string, data []byte) error
	remove(ctx context.Context, key string) error
}

func (s settings) destination(m *Module) destination {
	if s.Kind == kindWebDAV {
		return &folder{client: m.client, endpoint: s.Endpoint, user: s.AccessKey, password: s.SecretKey}
	}
	return &bucket{client: m.client, endpoint: s.Endpoint, region: s.Region, name: s.Bucket, pathStyle: s.PathStyle,
		access: s.AccessKey, secret: s.SecretKey, now: m.now}
}

// place names where s puts backups, for the log.
func (s settings) place() string {
	if s.Kind == kindWebDAV {
		if u, err := url.Parse(s.Endpoint); err == nil {
			return u.Host
		}
	}
	return s.Bucket
}

// load returns the settings, the zero value before any were saved.
func (m *Module) load(ctx context.Context) (settings, error) {
	var s settings
	var enabled, pathStyle int
	var sealed []byte
	err := m.db.QueryRowContext(ctx,
		"SELECT enabled, kind, endpoint, region, bucket, prefix, path_style, access_key, secret_key FROM offsite_settings WHERE id = 1").
		Scan(&enabled, &s.Kind, &s.Endpoint, &s.Region, &s.Bucket, &s.Prefix, &pathStyle, &s.AccessKey, &sealed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return settings{Kind: kindS3}, nil
	case err != nil:
		return s, err
	}
	s.Enabled, s.PathStyle = enabled == 1, pathStyle == 1
	if sealed != nil {
		secret, err := m.secrets.Open(sealed, secretContext)
		if err != nil {
			return s, err
		}
		s.SecretKey = string(secret)
	}
	return s, nil
}

// OffsiteSettingsBody is the settings as the API shows them, with how the
// last copy went; the name is the schema's in the OpenAPI document, beside
// other modules'.
type OffsiteSettingsBody struct { //nolint:revive // the schema's name
	Enabled    bool       `json:"enabled"`
	Kind       string     `json:"kind" enum:"s3,webdav" doc:"s3: an S3 compatible bucket; webdav: a folder of a WebDAV server, with accessKey and secretKey as the user name and password"`
	Endpoint   string     `json:"endpoint" maxLength:"512" doc:"The address of the S3 interface, as https://s3.us-east-1.amazonaws.com or https://<account>.r2.cloudflarestorage.com; or of the WebDAV folder"`
	Region     string     `json:"region" maxLength:"64" doc:"The region of the bucket; auto for Cloudflare R2"`
	Bucket     string     `json:"bucket" maxLength:"255"`
	Prefix     string     `json:"prefix" maxLength:"255" doc:"Put before the name of each backup, as jec/"`
	PathStyle  bool       `json:"pathStyle" doc:"The bucket in the path of the address rather than in its host, as MinIO has it"`
	AccessKey  string     `json:"accessKey" maxLength:"255"`
	HasSecret  bool       `json:"hasSecret" doc:"A secret key is set; it is never shown"`
	Copied     int        `json:"copied" doc:"The backups copied so far"`
	LastCopy   *time.Time `json:"lastCopy,omitempty" doc:"When a backup was last copied, or tried to be"`
	LastError  string     `json:"lastError,omitempty" doc:"Why the last copy failed, as the bucket said"`
	BackupsOff bool       `json:"backupsOff" doc:"No daily backups are made to copy: they are switched on in the settings of the data"`
}

type settingsOutput struct{ Body OffsiteSettingsBody }

type settingsInput struct {
	Body struct {
		Enabled   bool    `json:"enabled"`
		Kind      string  `json:"kind,omitempty" enum:"s3,webdav" doc:"s3 if left out"`
		Endpoint  string  `json:"endpoint" maxLength:"512"`
		Region    string  `json:"region" maxLength:"64"`
		Bucket    string  `json:"bucket" maxLength:"255"`
		Prefix    string  `json:"prefix" maxLength:"255"`
		PathStyle bool    `json:"pathStyle"`
		AccessKey string  `json:"accessKey" maxLength:"255"`
		SecretKey *string `json:"secretKey,omitempty" maxLength:"1024" doc:"Replaces the secret key when given"`
	}
}

func (m *Module) register(a huma.API) {
	tags := []string{"offsite"}
	only := map[string]any{auth.SessionOnly: true}
	huma.Register(a, huma.Operation{
		OperationID: "offsite-settings", Method: http.MethodGet, Path: "/settings", Tags: tags, Metadata: only,
		Summary: "Get where the daily backups are copied to, and how the last copy went",
	}, func(ctx context.Context, _ *struct{}) (*settingsOutput, error) {
		return m.output(ctx)
	})
	huma.Register(a, huma.Operation{
		OperationID: "offsite-save", Method: http.MethodPut, Path: "/settings", Tags: tags, Metadata: only,
		Summary:     "Save where the daily backups are copied to",
		Description: "Switched on, the bucket or folder is tried first: a small file is written there and deleted again.",
	}, m.save)
}

func (m *Module) output(ctx context.Context) (*settingsOutput, error) {
	s, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	b := OffsiteSettingsBody{
		Enabled: s.Enabled, Kind: s.Kind, Endpoint: s.Endpoint, Region: s.Region, Bucket: s.Bucket, Prefix: s.Prefix, PathStyle: s.PathStyle,
		AccessKey: s.AccessKey, HasSecret: s.SecretKey != "",
	}
	if err := m.db.QueryRowContext(ctx, "SELECT count(*) FROM offsite_uploads").Scan(&b.Copied); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if !m.lastAt.IsZero() {
		at := m.lastAt.UTC()
		b.LastCopy, b.LastError = &at, m.lastErr
	}
	m.mu.Unlock()
	if m.call != nil {
		var system struct {
			AutoBackup struct {
				Keep int `json:"keep"`
			} `json:"autoBackup"`
		}
		if err := m.call(ctx, "offsite", http.MethodGet, "/system", nil, &system); err == nil {
			b.BackupsOff = system.AutoBackup.Keep == 0
		}
	}
	return &settingsOutput{Body: b}, nil
}

// save checks and stores the settings: switched on, the bucket must take
// a file.
func (m *Module) save(ctx context.Context, in *settingsInput) (*settingsOutput, error) {
	old, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	b := in.Body
	s := settings{
		Enabled: b.Enabled, Kind: kindS3, Endpoint: strings.TrimRight(strings.TrimSpace(b.Endpoint), "/"), Region: strings.TrimSpace(b.Region),
		Bucket: strings.TrimSpace(b.Bucket), Prefix: strings.TrimLeft(strings.TrimSpace(b.Prefix), "/"), PathStyle: b.PathStyle,
		AccessKey: strings.TrimSpace(b.AccessKey), SecretKey: old.SecretKey,
	}
	if b.Kind == kindWebDAV {
		s.Kind = kindWebDAV
	}
	if b.SecretKey != nil {
		s.SecretKey = strings.TrimSpace(*b.SecretKey)
	}
	if s.Region == "" {
		s.Region = "us-east-1"
	}
	if s.Enabled {
		if err := m.check(ctx, s); err != nil {
			return nil, err
		}
	}
	var sealed []byte
	if s.SecretKey != "" {
		sealed = m.secrets.Seal([]byte(s.SecretKey), secretContext)
	}
	enabled, pathStyle := 0, 0
	if s.Enabled {
		enabled = 1
	}
	if s.PathStyle {
		pathStyle = 1
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO offsite_settings (id, enabled, kind, endpoint, region, bucket, prefix, path_style, access_key, secret_key, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled, kind = excluded.kind, endpoint = excluded.endpoint,
		region = excluded.region, bucket = excluded.bucket, prefix = excluded.prefix, path_style = excluded.path_style,
		access_key = excluded.access_key, secret_key = excluded.secret_key, updated_at = excluded.updated_at`,
		enabled, s.Kind, s.Endpoint, s.Region, s.Bucket, s.Prefix, pathStyle, s.AccessKey, sealed, m.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	if s.Enabled {
		m.trigger("copy")
	}
	return m.output(ctx)
}

// check tells what is wrong with settings to switch on: a field missing,
// or a bucket that does not take a file.
func (m *Module) check(ctx context.Context, s settings) error {
	invalid := api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
	if u, err := url.Parse(s.Endpoint); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		invalid = invalid.WithField("body.endpoint", i18n.OffsiteErrorEndpoint)
	}
	required := map[string]string{"body.bucket": s.Bucket, "body.accessKey": s.AccessKey, "body.secretKey": s.SecretKey}
	if s.Kind == kindWebDAV {
		delete(required, "body.bucket") // the address names the folder
	}
	for field, v := range required {
		if v == "" {
			invalid = invalid.WithField(field, i18n.KernelErrorFieldRequired)
		}
	}
	if len(invalid.Fields) > 0 {
		return invalid
	}
	b := s.destination(m)
	key := s.Prefix + "jec-write-test.txt"
	if err := b.put(ctx, key, []byte("JEC can write here.\n")); err != nil {
		return api.NewError(http.StatusUnprocessableEntity, i18n.OffsiteErrorWrite, map[string]any{"message": err.Error()})
	}
	_ = b.remove(ctx, key) // a key without the right to delete keeps the file
	return nil
}

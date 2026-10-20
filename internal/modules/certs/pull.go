package certs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
)

// Pulling (PLAN.md 4.6): a server JEC cannot reach, as one behind NAT,
// fetches the version in use of a certificate itself, as a cron job does,
// with a key of that certificate in the Authorization header, never in the
// address, which logs keep. Making a key takes the password; a new one
// ends the old, and the audit log records each fetch.

// pullPrefix starts every pull key: not jec_, which API tokens start with.
const pullPrefix = "jecpull_"

// pullFiles are the files a server can fetch.
var pullFiles = []string{"fullchain.pem", "privkey.pem", "cert.pem", "chain.pem"}

// PullBody tells of the pull address of a certificate.
type PullBody struct {
	Enabled   bool       `json:"enabled"`
	CreatedAt *time.Time `json:"createdAt,omitempty" doc:"When the key was made"`
	UsedAt    *time.Time `json:"usedAt,omitempty" doc:"When a server last fetched a file with it"`
	Key       string     `json:"key,omitempty" doc:"The key, only in the answer that made it"`
}

type pullOutput struct{ Body PullBody }

type pullKeyInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Password string `json:"password" minLength:"1" maxLength:"1024" doc:"The password of the signed-in account, asked again: the key fetches the private key"`
	}
}

type pullFileInput struct {
	File          string `path:"file" enum:"fullchain.pem,privkey.pem,cert.pem,chain.pem"`
	Authorization string `header:"Authorization" doc:"Bearer and the certificate's pull key"`
	IfNoneMatch   string `header:"If-None-Match" doc:"The ETag of what the server has, to fetch only a new version"`
}

type pullFileOutput struct {
	Status       int
	ContentType  string `header:"Content-Type"`
	ETag         string `header:"ETag"`
	CacheControl string `header:"Cache-Control"`
	Body         []byte
}

func registerPullAPI(a huma.API, s *service, confirm func(context.Context, string) error) {
	tags := []string{"certificates"}
	huma.Register(a, huma.Operation{
		OperationID: "certs-pull", Method: http.MethodGet, Path: "/certificates/{id}/pull", Tags: tags,
		Summary: "Tell whether servers can fetch a certificate themselves",
	}, func(ctx context.Context, in *idInput) (*pullOutput, error) {
		return s.pullState(ctx, in.ID)
	})
	huma.Register(a, huma.Operation{
		OperationID: "certs-pull-key", Method: http.MethodPost, Path: "/certificates/{id}/pull", Tags: tags,
		Metadata: map[string]any{auth.SessionOnly: true},
		Summary:  "Make the key servers fetch a certificate with, once the password is confirmed; the old one stops working",
	}, func(ctx context.Context, in *pullKeyInput) (*pullOutput, error) {
		s.named(ctx, in.ID)
		if err := confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		if _, err := s.load(ctx, s.db, in.ID); err != nil {
			return nil, serviceError(err)
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		key := pullPrefix + base64.RawURLEncoding.EncodeToString(b)
		hash := sha256.Sum256([]byte(key))
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO certs_pull (certificate_id, key_hash, created_at) VALUES (?, ?, ?)
			 ON CONFLICT (certificate_id) DO UPDATE SET key_hash = excluded.key_hash, created_at = excluded.created_at, used_at = NULL`,
			in.ID, hash[:], s.now().UnixMilli()); err != nil {
			return nil, err
		}
		out, err := s.pullState(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		out.Body.Key = key
		return out, nil
	})
	huma.Register(a, huma.Operation{
		OperationID: "certs-pull-off", Method: http.MethodDelete, Path: "/certificates/{id}/pull", Tags: tags, DefaultStatus: http.StatusNoContent,
		Summary: "Stop servers fetching a certificate: its key stops working",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		s.named(ctx, in.ID)
		_, err := s.db.ExecContext(ctx, "DELETE FROM certs_pull WHERE certificate_id = ?", in.ID)
		return nil, err
	})
	huma.Register(a, huma.Operation{
		OperationID: "certs-pull-file", Method: http.MethodGet, Path: "/pull/{file}", Tags: tags, Security: api.Public,
		Metadata:    map[string]any{api.Audited: true, api.AnyClient: true},
		Summary:     "Fetch a file of the version in use of a certificate, with its pull key",
		Description: "The key comes as Authorization: Bearer <key>. With If-None-Match and the ETag fetched before, an unchanged version answers 304.",
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{
			"application/x-pem-file": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}},
		}}},
	}, func(ctx context.Context, in *pullFileInput) (*pullFileOutput, error) {
		return s.pull(ctx, in)
	})
}

// pullState tells of the pull address of certificate id.
func (s *service) pullState(ctx context.Context, id int64) (*pullOutput, error) {
	out := &pullOutput{}
	var created int64
	var used sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT created_at, used_at FROM certs_pull WHERE certificate_id = ?", id).Scan(&created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	t := time.UnixMilli(created).UTC()
	out.Body.Enabled, out.Body.CreatedAt = true, &t
	if used.Valid {
		u := time.UnixMilli(used.Int64).UTC()
		out.Body.UsedAt = &u
	}
	return out, nil
}

// pull answers a server's fetch of a file of the certificate its key is
// for. A key that is no one's answers as a missing page does.
func (s *service) pull(ctx context.Context, in *pullFileInput) (*pullFileOutput, error) {
	notFound := api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
	scheme, key, _ := strings.Cut(strings.TrimSpace(in.Authorization), " ")
	key = strings.TrimSpace(key)
	if !strings.EqualFold(scheme, "Bearer") || !strings.HasPrefix(key, pullPrefix) || !slices.Contains(pullFiles, in.File) {
		return nil, notFound
	}
	hash := sha256.Sum256([]byte(key))
	var id int64
	err := s.db.QueryRowContext(ctx, "SELECT certificate_id FROM certs_pull WHERE key_hash = ?", hash[:]).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound
	}
	if err != nil {
		return nil, err
	}
	c, err := s.load(ctx, s.db, id)
	if err != nil {
		return nil, serviceError(err)
	}
	if c.Current == nil {
		return nil, notFound
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE certs_pull SET used_at = ? WHERE certificate_id = ?", s.now().UnixMilli(), id); err != nil {
		return nil, err
	}
	etag := `"` + c.Current.Fingerprint + `"`
	out := &pullFileOutput{Status: http.StatusOK, ETag: etag, CacheControl: "no-store"}
	if strings.TrimSpace(in.IfNoneMatch) == etag {
		out.Status = http.StatusNotModified
		return out, nil
	}
	files, _, _, err := s.versionFiles(ctx, id, c.Current.ID, nil)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.name == in.File {
			out.ContentType, out.Body = "application/x-pem-file", f.data
		}
	}
	return out, nil
}

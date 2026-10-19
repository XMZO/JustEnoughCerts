package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

// API tokens let programs use the API as their account (PLAN.md 10): they
// come in the Authorization header as bearer tokens, in place of the
// session cookie. A read token takes GET and HEAD alone. No token manages
// the account itself, its password, two-factor authentication or tokens,
// which takes a session: a token that leaks cannot take the account.
const (
	// tokenPrefix starts every token, so that JEC tells its own from the
	// bearer tokens a proxy in front of it may pass on, and secret
	// scanners know them.
	tokenPrefix = "jec_"
	scopeRead   = "read"
	scopeWrite  = "write"
	// maxTokens bounds the tokens of an account.
	maxTokens = 50
	// touchEvery bounds how often the last use of a token is written.
	touchEvery = time.Minute
)

// SessionOnly is the metadata of the operations no API token may use:
// set it to true on an operation that manages the account or JEC itself.
const SessionOnly = "sessionOnly"

const sessionOnly = SessionOnly

// bearer returns the API token of the request of ctx, if it has one.
func bearer(ctx huma.Context) (string, bool) {
	scheme, token, ok := strings.Cut(ctx.Header("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !ok || !strings.EqualFold(scheme, "Bearer") || !strings.HasPrefix(token, tokenPrefix) {
		return "", false
	}
	return token, true
}

// authorizeToken checks the API token of a request for its operation, and
// returns the session it stands for, or writes why not.
func (s *Service) authorizeToken(ctx huma.Context, token string) (session, bool) {
	sess, ok := s.lookupToken(ctx.Context(), token)
	op := ctx.Operation()
	switch {
	case !ok:
		api.WriteError(ctx, api.NewError(http.StatusUnauthorized, i18n.KernelErrorTokenInvalid, nil))
		return session{}, false
	case op != nil && op.Metadata[sessionOnly] == true:
		api.WriteError(ctx, api.NewError(http.StatusForbidden, i18n.KernelErrorTokenSessionOnly, nil))
		return session{}, false
	case sess.scope == scopeRead && ctx.Method() != http.MethodGet && ctx.Method() != http.MethodHead:
		api.WriteError(ctx, api.NewError(http.StatusForbidden, i18n.KernelErrorTokenReadOnly, nil))
		return session{}, false
	}
	return sess, true
}

// lookupToken returns the session an API token stands for, and notes that
// it was used.
func (s *Service) lookupToken(ctx context.Context, token string) (session, bool) {
	hash := sha256.Sum256([]byte(token))
	var sess session
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT t.id, t.name, t.scope, u.id, u.username, coalesce(u.locale, '')
		   FROM kernel_api_tokens t JOIN kernel_users u ON u.id = t.user_id
		  WHERE t.token_hash = ?`, hash[:]).Scan(&id, &sess.token, &sess.scope, &sess.user.ID, &sess.user.Username, &sess.user.Locale)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.log.Error("look up API token", "err", err)
		}
		return session{}, false
	}
	s.touch(ctx, id)
	return sess, true
}

// touch writes when token id was used, once a minute at most.
func (s *Service) touch(ctx context.Context, id int64) {
	now := s.now()
	s.mu.Lock()
	if now.Sub(s.touched[id]) < touchEvery {
		s.mu.Unlock()
		return
	}
	s.touched[id] = now
	s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx, "UPDATE kernel_api_tokens SET last_used_at = ? WHERE id = ?", timestamp(now), id); err != nil {
		s.log.Warn("note the use of an API token", "err", err)
	}
}

// TokenFrom returns the name of the API token the request of ctx came
// with; "" for a session.
func TokenFrom(ctx context.Context) string {
	s, _ := ctx.Value(sessionKey{}).(session)
	return s.token
}

// TokenBody is an API token, as the API tells of it: never its value,
// which only its creation returns.
type TokenBody struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope" enum:"read,write" doc:"read: GET and HEAD alone; write: any operation but those managing the account"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty" doc:"When it was last used, to the minute"`
}

type tokensOutput struct {
	Body struct {
		Tokens []TokenBody `json:"tokens" doc:"The newest first"`
	}
}

type createTokenInput struct {
	Body struct {
		Name     string `json:"name" minLength:"1" maxLength:"64" doc:"What the token is for, such as a script"`
		Scope    string `json:"scope" enum:"read,write"`
		Password string `json:"password" maxLength:"1024" doc:"The account's password, to confirm"`
	}
}

type createTokenOutput struct {
	Body struct {
		Token  string    `json:"token" doc:"The token, which no later answer shows again"`
		Detail TokenBody `json:"detail"`
	}
}

type deleteTokenInput struct {
	ID int64 `path:"id"`
}

// registerTokens adds the operations on API tokens to a, for sessions only.
func (s *Service) registerTokens(a huma.API, tags []string) {
	only := map[string]any{sessionOnly: true}
	huma.Register(a, huma.Operation{
		OperationID: "tokens-list", Method: http.MethodGet, Path: "/account/tokens", Tags: tags, Metadata: only,
		Summary: "List the API tokens of the account",
	}, s.listTokens)
	huma.Register(a, huma.Operation{
		OperationID: "tokens-create", Method: http.MethodPost, Path: "/account/tokens", Tags: tags, Metadata: only,
		Summary: "Make an API token, once the password is confirmed", DefaultStatus: http.StatusCreated,
	}, s.createToken)
	huma.Register(a, huma.Operation{
		OperationID: "tokens-delete", Method: http.MethodDelete, Path: "/account/tokens/{id}", Tags: tags, Metadata: only,
		Summary: "Delete an API token, which stops working at once", DefaultStatus: http.StatusNoContent,
	}, s.deleteToken)
}

func (s *Service) listTokens(ctx context.Context, _ *struct{}) (*tokensOutput, error) {
	u, _ := UserFrom(ctx)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, scope, created_at, last_used_at FROM kernel_api_tokens WHERE user_id = ? ORDER BY id DESC`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &tokensOutput{}
	out.Body.Tokens = []TokenBody{}
	for rows.Next() {
		var t TokenBody
		var created string
		var used sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &t.Scope, &created, &used); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, created)
		if used.Valid {
			at, _ := time.Parse(time.RFC3339, used.String)
			t.LastUsedAt = &at
		}
		out.Body.Tokens = append(out.Body.Tokens, t)
	}
	return out, rows.Err()
}

func (s *Service) createToken(ctx context.Context, in *createTokenInput) (*createTokenOutput, error) {
	if err := s.ConfirmPassword(ctx, in.Body.Password); err != nil {
		return nil, err
	}
	u, _ := UserFrom(ctx)
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM kernel_api_tokens WHERE user_id = ?", u.ID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= maxTokens {
		return nil, api.NewError(http.StatusConflict, i18n.KernelErrorTokenLimit, map[string]any{"count": maxTokens})
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	token := tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	now := s.now()
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO kernel_api_tokens (user_id, name, token_hash, scope, created_at) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		u.ID, strings.TrimSpace(in.Body.Name), hash[:], in.Body.Scope, timestamp(now)).Scan(&id)
	if err != nil {
		return nil, err
	}
	s.log.Info("API token created", "username", u.Username, "token", in.Body.Name, "scope", in.Body.Scope)
	out := &createTokenOutput{}
	out.Body.Token = token
	out.Body.Detail = TokenBody{ID: id, Name: strings.TrimSpace(in.Body.Name), Scope: in.Body.Scope, CreatedAt: now.UTC().Truncate(time.Second)}
	return out, nil
}

func (s *Service) deleteToken(ctx context.Context, in *deleteTokenInput) (*struct{}, error) {
	u, _ := UserFrom(ctx)
	res, err := s.db.ExecContext(ctx, "DELETE FROM kernel_api_tokens WHERE id = ? AND user_id = ?", in.ID, u.ID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorTokenNotFound, nil)
	}
	return nil, nil
}

// Package auth handles the accounts of JEC: the first-run setup, signing in
// and out, and the session behind every API request.
//
// Until the first account exists, the server logs a one-time setup token,
// and only someone who can read the log can create that account: nobody
// can claim a freshly exposed server first. Sessions are random tokens in
// an HttpOnly cookie, stored as SHA-256 hashes.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

// CookieName is the session cookie.
const CookieName = "jec_session"

const (
	sessionTTL  = 30 * 24 * time.Hour
	maxFailures = 5
	lockout     = 15 * time.Minute
)

// loginFailed is the answer to a wrong username or password; it says how
// many failures lock the client out, and for how long.
func loginFailed() error {
	return api.NewError(http.StatusUnauthorized, i18n.KernelErrorLoginFailed,
		map[string]any{"attempts": maxFailures, "minutes": int(lockout.Minutes())})
}

// Options configure the Service.
type Options struct {
	// CookiePath scopes the session cookie: the base path, or "/".
	CookiePath string
	// Secure restricts the cookie to HTTPS; set it when users reach JEC
	// over HTTPS, as an https base URL says.
	Secure bool
}

// Service is the account system.
type Service struct {
	db    *sql.DB
	log   *slog.Logger
	opts  Options
	now   func() time.Time
	limit *limiter

	mu         sync.Mutex
	setupToken string // set while no account exists
}

// New starts the account system on db, whose kernel migrations are applied.
// Without any account it makes a setup token and logs it.
func New(ctx context.Context, db *sql.DB, log *slog.Logger, opts Options) (*Service, error) {
	s := &Service{db: db, log: log, opts: opts, now: time.Now, limit: newLimiter(maxFailures, lockout)}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM kernel_users").Scan(&n); err != nil {
		return nil, fmt.Errorf("count accounts: %w", err)
	}
	if n == 0 {
		b := make([]byte, 20)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		s.setupToken = strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
		log.Warn("JEC has no account yet: open the web UI and enter this setup token", "setup_token", s.setupToken)
	}
	return s, nil
}

// User is the account a request is signed in as.
type User struct {
	ID       int64
	Username string
	Locale   string
}

type (
	sessionKey struct{}
	clientKey  struct{}
)

type session struct {
	user      User
	tokenHash []byte
}

// UserFrom returns the account the request of ctx is signed in as.
func UserFrom(ctx context.Context) (User, bool) {
	s, ok := ctx.Value(sessionKey{}).(session)
	return s.user, ok
}

func clientFrom(ctx context.Context) string {
	c, _ := ctx.Value(clientKey{}).(string)
	return c
}

// Middleware loads the session of every request and turns away a request
// without one unless its operation is api.Public.
func (s *Service) Middleware(ctx huma.Context, next func(huma.Context)) {
	client := ctx.RemoteAddr()
	if host, _, err := net.SplitHostPort(client); err == nil {
		client = host
	}
	ctx = huma.WithValue(ctx, clientKey{}, client)
	if sess, ok := s.lookup(ctx.Context(), cookie(ctx)); ok {
		ctx = huma.WithValue(ctx, sessionKey{}, sess)
	} else if op := ctx.Operation(); op == nil || op.Security == nil || len(op.Security) > 0 {
		api.WriteError(ctx, api.NewError(http.StatusUnauthorized, i18n.KernelErrorUnauthorized, nil))
		return
	}
	next(ctx)
}

func cookie(ctx huma.Context) string {
	cookies, err := http.ParseCookie(ctx.Header("Cookie"))
	if err != nil {
		return ""
	}
	for _, c := range cookies {
		if c.Name == CookieName {
			return c.Value
		}
	}
	return ""
}

func (s *Service) lookup(ctx context.Context, token string) (session, bool) {
	if token == "" {
		return session{}, false
	}
	hash := sha256.Sum256([]byte(token))
	sess := session{tokenHash: hash[:]}
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.username, coalesce(u.locale, '')
		   FROM kernel_sessions s JOIN kernel_users u ON u.id = s.user_id
		  WHERE s.token_hash = ? AND s.expires_at > ?`,
		hash[:], timestamp(s.now())).Scan(&sess.user.ID, &sess.user.Username, &sess.user.Locale)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.log.Error("look up session", "err", err)
		}
		return session{}, false
	}
	return sess, true
}

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Account is what the API tells about the signed-in account.
type Account struct {
	Username string `json:"username"`
	Locale   string `json:"locale,omitempty" doc:"BCP 47 tag of the language chosen for the account"`
}

type setupStatus struct {
	Body struct {
		Required bool `json:"required" doc:"No account exists yet: the setup wizard has to run"`
	}
}

// The password limits are MinPasswordLength and MaxPasswordLength.
type setupInput struct {
	Body struct {
		Token    string `json:"token" maxLength:"128" doc:"The one-time setup token from the server log"`
		Username string `json:"username" minLength:"1" maxLength:"64" pattern:"^[\\p{L}\\p{N}._-]+$" doc:"Letters, digits, dots, underscores and hyphens"`
		Password string `json:"password" minLength:"8" maxLength:"1024"`
		Locale   string `json:"locale,omitempty" maxLength:"35" doc:"BCP 47 tag of the language the account starts with"`
	}
}

type signInInput struct {
	Body struct {
		Username string `json:"username" maxLength:"64"`
		Password string `json:"password" maxLength:"1024"`
	}
}

type sessionOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      Account
}

type accountOutput struct {
	Body Account
}

type signOutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

// Register adds the account operations to a.
func (s *Service) Register(a huma.API) {
	tags := []string{"auth"}
	huma.Register(a, huma.Operation{
		OperationID: "get-setup", Method: http.MethodGet, Path: "/setup", Tags: tags, Security: api.Public,
		Summary: "Tell whether the first account still has to be created",
	}, s.getSetup)
	huma.Register(a, huma.Operation{
		OperationID: "setup", Method: http.MethodPost, Path: "/setup", Tags: tags, Security: api.Public,
		Summary: "Create the first account with the setup token, and sign in", DefaultStatus: http.StatusCreated,
	}, s.setup)
	huma.Register(a, huma.Operation{
		OperationID: "sign-in", Method: http.MethodPost, Path: "/session", Tags: tags, Security: api.Public,
		Summary: "Sign in",
	}, s.signIn)
	huma.Register(a, huma.Operation{
		OperationID: "get-session", Method: http.MethodGet, Path: "/session", Tags: tags,
		Summary: "Get the signed-in account",
	}, s.getSession)
	huma.Register(a, huma.Operation{
		OperationID: "sign-out", Method: http.MethodDelete, Path: "/session", Tags: tags,
		Summary: "Sign out", DefaultStatus: http.StatusNoContent,
	}, s.signOut)
}

func (s *Service) getSetup(context.Context, *struct{}) (*setupStatus, error) {
	out := &setupStatus{}
	s.mu.Lock()
	out.Body.Required = s.setupToken != ""
	s.mu.Unlock()
	return out, nil
}

func (s *Service) setup(ctx context.Context, in *setupInput) (*sessionOutput, error) {
	client := clientFrom(ctx)
	if err := s.throttle(client); err != nil {
		return nil, err
	}
	s.mu.Lock()
	want := s.setupToken
	s.mu.Unlock()
	if want == "" {
		return nil, api.NewError(http.StatusConflict, i18n.KernelErrorSetupDone, nil)
	}
	given := strings.ToLower(strings.TrimSpace(in.Body.Token))
	if subtle.ConstantTimeCompare([]byte(given), []byte(want)) != 1 {
		s.limit.fail(client, s.now())
		return nil, api.NewError(http.StatusForbidden, i18n.KernelErrorSetupTokenInvalid, nil)
	}
	var locale sql.NullString
	if in.Body.Locale != "" {
		tag, err := language.Parse(in.Body.Locale)
		if err != nil {
			e := api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
			e.Fields = []api.FieldError{{Location: "body.locale", Code: i18n.KernelErrorInvalidField}}
			return nil, e
		}
		locale = sql.NullString{String: tag.String(), Valid: true}
	}
	hash, err := hashPassword(in.Body.Password)
	if err != nil {
		return nil, err
	}

	// The write lock of the transaction serializes concurrent setups: the
	// second one finds the account the first one made.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM kernel_users").Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, api.NewError(http.StatusConflict, i18n.KernelErrorSetupDone, nil)
	}
	now := timestamp(s.now())
	res, err := tx.ExecContext(ctx,
		"INSERT INTO kernel_users (username, password_hash, locale, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		in.Body.Username, hash, locale, now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	token, expires, err := s.newSession(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.setupToken = ""
	s.mu.Unlock()
	s.limit.reset(client)
	s.log.Info("first account created", "username", in.Body.Username)
	return &sessionOutput{SetCookie: s.cookie(token, expires), Body: Account{Username: in.Body.Username, Locale: locale.String}}, nil
}

func (s *Service) signIn(ctx context.Context, in *signInInput) (*sessionOutput, error) {
	client := clientFrom(ctx)
	if err := s.throttle(client); err != nil {
		return nil, err
	}
	var id int64
	var hash string
	var locale sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id, password_hash, locale FROM kernel_users WHERE username = ?",
		in.Body.Username).Scan(&id, &hash, &locale)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, _ = checkPassword(dummyHash(), in.Body.Password) // take as long as a wrong password
		s.limit.fail(client, s.now())
		return nil, loginFailed()
	case err != nil:
		return nil, err
	}
	ok, err := checkPassword(hash, in.Body.Password)
	if err != nil {
		return nil, err
	}
	if !ok {
		s.limit.fail(client, s.now())
		return nil, loginFailed()
	}
	s.limit.reset(client)
	if _, err := s.db.ExecContext(ctx, "DELETE FROM kernel_sessions WHERE expires_at <= ?", timestamp(s.now())); err != nil {
		s.log.Warn("remove expired sessions", "err", err)
	}
	token, expires, err := s.newSession(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	return &sessionOutput{SetCookie: s.cookie(token, expires), Body: Account{Username: in.Body.Username, Locale: locale.String}}, nil
}

func (s *Service) getSession(ctx context.Context, _ *struct{}) (*accountOutput, error) {
	u, _ := UserFrom(ctx)
	return &accountOutput{Body: Account{Username: u.Username, Locale: u.Locale}}, nil
}

func (s *Service) signOut(ctx context.Context, _ *struct{}) (*signOutOutput, error) {
	if sess, ok := ctx.Value(sessionKey{}).(session); ok {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM kernel_sessions WHERE token_hash = ?", sess.tokenHash); err != nil {
			return nil, err
		}
	}
	return &signOutOutput{SetCookie: s.cookie("", time.Time{})}, nil
}

// ConfirmPassword checks password against the account ctx is signed in
// as: a second look before something sensitive, such as handing out a
// private key. Wrong passwords count toward the same lockout as signing
// in, and are reported as a wrong body.password.
func (s *Service) ConfirmPassword(ctx context.Context, password string) error {
	client := clientFrom(ctx)
	if err := s.throttle(client); err != nil {
		return err
	}
	u, ok := UserFrom(ctx)
	if !ok {
		return api.NewError(http.StatusUnauthorized, i18n.KernelErrorUnauthorized, nil)
	}
	var hash string
	if err := s.db.QueryRowContext(ctx, "SELECT password_hash FROM kernel_users WHERE id = ?", u.ID).Scan(&hash); err != nil {
		return err
	}
	ok, err := checkPassword(hash, password)
	if err != nil {
		return err
	}
	if !ok {
		s.limit.fail(client, s.now())
		return api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorPasswordWrong, nil).
			WithField("body.password", i18n.KernelErrorPasswordWrong)
	}
	s.limit.reset(client)
	return nil
}

// throttle refuses a client that failed too often lately.
func (s *Service) throttle(client string) error {
	wait := s.limit.wait(client, s.now())
	if wait <= 0 {
		return nil
	}
	e := api.NewError(http.StatusTooManyRequests, i18n.KernelErrorTooManyAttempts,
		map[string]any{"minutes": int(math.Ceil(wait.Minutes()))})
	return huma.ErrorWithHeaders(e, http.Header{"Retry-After": {strconv.Itoa(int(math.Ceil(wait.Seconds())))}})
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Service) newSession(ctx context.Context, db execer, userID int64) (string, time.Time, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	now := s.now()
	expires := now.Add(sessionTTL)
	_, err := db.ExecContext(ctx,
		"INSERT INTO kernel_sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)",
		hash[:], userID, timestamp(now), timestamp(expires))
	return token, expires, err
}

// cookie returns the session cookie holding value until expires; an empty
// value deletes it. Secure follows Options: a browser drops a Secure cookie
// that arrives over plain HTTP, which would lock out a server used without
// TLS, such as one on a LAN.
func (s *Service) cookie(value string, expires time.Time) http.Cookie {
	c := http.Cookie{ //nolint:gosec // G124: Secure depends on the deployment, see above
		Name:     CookieName,
		Value:    value,
		Path:     s.opts.CookiePath,
		HttpOnly: true,
		Secure:   s.opts.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
	if value != "" {
		c.Expires = expires.UTC()
		c.MaxAge = int(expires.Sub(s.now()).Seconds())
	}
	return c
}

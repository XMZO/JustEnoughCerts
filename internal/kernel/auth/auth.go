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
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/clientip"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
)

// CookieName is the session cookie.
const CookieName = "jec_session"

const (
	// defaultSessionDays is how long a sign-in lasts until the account
	// says otherwise.
	defaultSessionDays = 7
	maxFailures        = 5
	lockout            = 15 * time.Minute
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
	// Secrets seals the secrets of two-factor authentication.
	Secrets *secrets.Scope
	// Clients finds the client of a request, behind trusted proxies too,
	// which sign-in lockouts go by. Nil means the peer.
	Clients *clientip.Resolver
}

// Service is the account system.
type Service struct {
	db    *sql.DB
	log   *slog.Logger
	opts  Options
	now   func() time.Time
	limit *limiter

	mu         sync.Mutex
	setupToken string                  // set while no account exists
	pending    map[int64]pendingSecret // new secrets of two-factor authentication, by account
}

// New starts the account system on db, whose kernel migrations are applied.
// Without any account it makes a setup token and logs it.
func New(ctx context.Context, db *sql.DB, log *slog.Logger, opts Options) (*Service, error) {
	s := &Service{db: db, log: log, opts: opts, now: time.Now, limit: newLimiter(maxFailures, lockout), pending: map[int64]pendingSecret{}}
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

// actor holds the account an operation signs in, as a sign-in or the
// setup does: the request has no session to tell it yet.
type actor struct{ username string }

type actorKey struct{}

// SignedInAs returns the account the operation of ctx signed in, once it
// ran; "" for none, as when the sign-in failed.
func SignedInAs(ctx context.Context) string {
	if a, ok := ctx.Value(actorKey{}).(*actor); ok {
		return a.username
	}
	return ""
}

func signedIn(ctx context.Context, username string) {
	if a, ok := ctx.Value(actorKey{}).(*actor); ok {
		a.username = username
	}
}

// ClientFrom returns the address of the client of the request of ctx, as
// the Middleware found it: behind trusted proxies, the one they forwarded
// for.
func ClientFrom(ctx context.Context) string { return clientFrom(ctx) }

func clientFrom(ctx context.Context) string {
	c, _ := ctx.Value(clientKey{}).(string)
	return c
}

// Middleware loads the session of every request and turns away a request
// without one unless its operation is api.Public.
func (s *Service) Middleware(ctx huma.Context, next func(huma.Context)) {
	ctx = huma.WithValue(ctx, clientKey{}, s.client(ctx))
	ctx = huma.WithValue(ctx, actorKey{}, &actor{})
	if sess, ok := s.lookup(ctx.Context(), cookie(ctx)); ok {
		ctx = huma.WithValue(ctx, sessionKey{}, sess)
	} else if op := ctx.Operation(); op == nil || op.Security == nil || len(op.Security) > 0 {
		api.WriteError(ctx, api.NewError(http.StatusUnauthorized, i18n.KernelErrorUnauthorized, nil))
		return
	}
	next(ctx)
}

// client is the address of the client of ctx: the peer, or behind trusted
// proxies the one they forwarded for.
func (s *Service) client(ctx huma.Context) string {
	var forwarded []string
	ctx.EachHeader(func(name, value string) {
		if strings.EqualFold(name, "X-Forwarded-For") {
			forwarded = append(forwarded, value)
		}
	})
	clients := s.opts.Clients
	if clients == nil {
		clients, _ = clientip.New("")
	}
	return clients.Client(ctx.RemoteAddr(), forwarded)
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
	Username          string     `json:"username"`
	Locale            string     `json:"locale,omitempty" doc:"BCP 47 tag of the language chosen for the account"`
	PasswordChangedAt *time.Time `json:"passwordChangedAt,omitempty" doc:"When the password last changed"`
	TwoFactor         bool       `json:"twoFactor" doc:"Signing in also takes a code of an authenticator app"`
	SessionDays       int        `json:"sessionDays" doc:"How many days a sign-in lasts"`
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
		Code     string `json:"code,omitempty" maxLength:"16" doc:"A code of the authenticator app, for an account with two-factor authentication on"`
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

// The limits of NewPassword are MinPasswordLength and MaxPasswordLength.
type passwordInput struct {
	Body struct {
		Password    string `json:"password" maxLength:"1024" doc:"The current password"`
		NewPassword string `json:"newPassword" minLength:"8" maxLength:"1024"`
	}
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
	huma.Register(a, huma.Operation{
		OperationID: "change-password", Method: http.MethodPut, Path: "/account/password", Tags: tags,
		Summary: "Change the password, which signs the account out everywhere else", DefaultStatus: http.StatusNoContent,
	}, s.changePassword)
	huma.Register(a, huma.Operation{
		OperationID: "update-account", Method: http.MethodPatch, Path: "/account", Tags: tags,
		Summary: "Change the settings of the account",
	}, s.updateAccount)
	huma.Register(a, huma.Operation{
		OperationID: "two-factor-secret", Method: http.MethodPost, Path: "/account/two-factor/secret", Tags: tags,
		Summary:     "Make a new secret for two-factor authentication",
		Description: "Once the password is confirmed. The secret waits ten minutes for a code of it, which turns two-factor authentication on.",
	}, s.setUpTwoFactor)
	huma.Register(a, huma.Operation{
		OperationID: "two-factor-enable", Method: http.MethodPost, Path: "/account/two-factor/enable", Tags: tags,
		Summary:       "Turn two-factor authentication on with a code of the new secret, which signs the account out everywhere else",
		DefaultStatus: http.StatusNoContent,
	}, s.enableTwoFactor)
	huma.Register(a, huma.Operation{
		OperationID: "two-factor-disable", Method: http.MethodPost, Path: "/account/two-factor/disable", Tags: tags,
		Summary: "Turn two-factor authentication off, once the password is confirmed", DefaultStatus: http.StatusNoContent,
	}, s.disableTwoFactor)
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
		`INSERT INTO kernel_users (username, password_hash, locale, created_at, updated_at, password_changed_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		in.Body.Username, hash, locale, now, now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	token, expires, err := s.newSession(ctx, tx, id, defaultSessionDays)
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
	signedIn(ctx, in.Body.Username)
	s.log.Info("first account created", "username", in.Body.Username)
	account, err := s.account(ctx, id)
	if err != nil {
		return nil, err
	}
	return &sessionOutput{SetCookie: s.cookie(token, expires), Body: account}, nil
}

func (s *Service) signIn(ctx context.Context, in *signInInput) (*sessionOutput, error) {
	client := clientFrom(ctx)
	if err := s.throttle(client); err != nil {
		return nil, err
	}
	var id, step int64
	var hash string
	var secret []byte
	var days int
	err := s.db.QueryRowContext(ctx, "SELECT id, password_hash, totp_secret, totp_step, session_days FROM kernel_users WHERE username = ?",
		in.Body.Username).Scan(&id, &hash, &secret, &step, &days)
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
	if len(secret) > 0 {
		if err := s.secondFactor(ctx, client, id, secret, step, in.Body.Code); err != nil {
			return nil, err
		}
	}
	s.limit.reset(client)
	if _, err := s.db.ExecContext(ctx, "DELETE FROM kernel_sessions WHERE expires_at <= ?", timestamp(s.now())); err != nil {
		s.log.Warn("remove expired sessions", "err", err)
	}
	token, expires, err := s.newSession(ctx, s.db, id, days)
	if err != nil {
		return nil, err
	}
	account, err := s.account(ctx, id)
	if err != nil {
		return nil, err
	}
	signedIn(ctx, account.Username)
	return &sessionOutput{SetCookie: s.cookie(token, expires), Body: account}, nil
}

func (s *Service) getSession(ctx context.Context, _ *struct{}) (*accountOutput, error) {
	u, _ := UserFrom(ctx)
	account, err := s.account(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &accountOutput{Body: account}, nil
}

// account reads what the API tells about account id.
func (s *Service) account(ctx context.Context, id int64) (Account, error) {
	var a Account
	var changed sql.NullString
	if err := s.db.QueryRowContext(ctx,
		"SELECT username, coalesce(locale, ''), password_changed_at, totp_secret IS NOT NULL, session_days FROM kernel_users WHERE id = ?", id).
		Scan(&a.Username, &a.Locale, &changed, &a.TwoFactor, &a.SessionDays); err != nil {
		return Account{}, err
	}
	if t, err := time.Parse(time.RFC3339, changed.String); err == nil {
		a.PasswordChangedAt = &t
	}
	return a, nil
}

// changePassword gives the signed-in account a new password once the
// current one is confirmed, and ends its other sessions: whoever else
// knew the old password is out.
func (s *Service) changePassword(ctx context.Context, in *passwordInput) (*struct{}, error) {
	if err := s.ConfirmPassword(ctx, in.Body.Password); err != nil {
		return nil, err
	}
	u, _ := UserFrom(ctx)
	sess, _ := ctx.Value(sessionKey{}).(session)
	hash, err := hashPassword(in.Body.NewPassword)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := timestamp(s.now())
	if _, err := tx.ExecContext(ctx, "UPDATE kernel_users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?",
		hash, now, now, u.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM kernel_sessions WHERE user_id = ? AND token_hash != ?", u.ID, sess.tokenHash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.log.Info("password changed", "username", u.Username)
	return nil, nil
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

// newSession signs userID in for days.
func (s *Service) newSession(ctx context.Context, db execer, userID int64, days int) (string, time.Time, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	now := s.now()
	expires := now.Add(time.Duration(max(days, 1)) * 24 * time.Hour)
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

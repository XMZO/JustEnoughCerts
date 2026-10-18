package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	jose "github.com/go-jose/go-jose/v4"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
)

// How a sign-in goes: the cookie that ties its return from the provider to
// the browser that started it, how long it may take, and how long a
// discovery document is kept.
const (
	stateCookie   = "jec_oidc"
	loginLifetime = 10 * time.Minute
	discoveryTTL  = time.Hour
)

// The reasons a sign-in fails, which the sign-in page explains: the
// sign-in took too long or came from another browser, the provider said
// no, the answer of the provider did not check out, the person is not
// among those allowed, JEC has no account yet, or it is switched off.
const (
	failExpired    = "expired"
	failDenied     = "denied"
	failInvalid    = "invalid"
	failNotAllowed = "not_allowed"
	failNoAccount  = "no_account"
	failOff        = "off"
)

// redirectOutput sends the browser on, with cookies.
type redirectOutput struct {
	Status    int           `json:"-"`
	Location  string        `header:"Location"`
	SetCookie []http.Cookie `header:"Set-Cookie"`
	CacheCtl  string        `header:"Cache-Control"`
}

type callbackInput struct {
	Code        string      `query:"code" maxLength:"4096"`
	State       string      `query:"state" maxLength:"256"`
	Error       string      `query:"error" maxLength:"256"`
	Description string      `query:"error_description" maxLength:"1024"`
	Cookie      http.Cookie `cookie:"jec_oidc"`
}

func (m *Module) registerFlow(a huma.API, tags []string) {
	huma.Register(a, huma.Operation{
		OperationID: "oidc-login", Method: http.MethodGet, Path: "/login", Tags: tags, Security: api.Public,
		Summary: "Go to the provider to sign in", DefaultStatus: http.StatusFound,
	}, m.login)
	huma.Register(a, huma.Operation{
		OperationID: "oidc-callback", Method: http.MethodGet, Path: "/callback", Tags: tags, Security: api.Public,
		Summary: "Come back from the provider, signed in", DefaultStatus: http.StatusFound,
		Metadata: map[string]any{api.Audited: true},
	}, m.callback)
}

// random returns 32 random bytes, in base64url.
func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashState(state string) []byte {
	sum := sha256.Sum256([]byte(state))
	return sum[:]
}

// pending is what a sign-in under way keeps, sealed, until it returns.
type pending struct {
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
}

// home is the address of the UI, where a sign-in ends, out of the address
// the provider sends people back to.
func home(s settings) string { return strings.TrimSuffix(s.RedirectURI, callbackPath) }

// cookiePath is where the state cookie goes: the API of the module.
func cookiePath(s settings) string {
	u, err := url.Parse(s.RedirectURI)
	if err != nil {
		return "/"
	}
	return strings.TrimSuffix(u.Path, "callback")
}

func secure(s settings) bool { return strings.HasPrefix(s.RedirectURI, "https://") }

// login starts a sign-in: it keeps a state, a nonce and a PKCE verifier
// for the return, ties the state to the browser with a cookie, and sends
// the browser to the provider.
func (m *Module) login(ctx context.Context, _ *struct{}) (*redirectOutput, error) {
	s, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	if !s.Enabled {
		return &redirectOutput{Status: http.StatusFound, Location: home(s) + "login?sso=" + failOff, CacheCtl: "no-store"}, nil
	}
	p, err := m.provider(ctx, s)
	if err != nil {
		m.log.Warn("oidc discovery", "issuer", s.Issuer, "err", err)
		return &redirectOutput{Status: http.StatusFound, Location: home(s) + "login?sso=" + failInvalid, CacheCtl: "no-store"}, nil
	}
	state, nonce, verifier := random(), random(), random()
	data, err := json.Marshal(pending{Nonce: nonce, Verifier: verifier})
	if err != nil {
		return nil, err
	}
	now := m.now()
	if _, err := m.db.ExecContext(ctx, "DELETE FROM oidc_logins WHERE created_at < ?", now.Add(-loginLifetime).UnixMilli()); err != nil {
		return nil, err
	}
	_, err = m.db.ExecContext(ctx, "INSERT INTO oidc_logins (state_hash, sealed, created_at) VALUES (?, ?, ?)",
		hashState(state), m.secrets.Seal(data, "oidc login"), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	c := http.Cookie{ //nolint:gosec // G124: Secure follows the scheme of the callback, as the session's follows the deployment
		Name: stateCookie, Value: state, Path: cookiePath(s), MaxAge: int(loginLifetime.Seconds()), HttpOnly: true, Secure: secure(s), SameSite: http.SameSiteLaxMode,
	}
	return &redirectOutput{
		Status: http.StatusFound, Location: authURL(p.d, s.ClientID, s.RedirectURI, state, nonce, verifier),
		SetCookie: []http.Cookie{c}, CacheCtl: "no-store",
	}, nil
}

// callback ends a sign-in: the state must be the browser's own and the
// sign-in recent, the code must give an ID token the provider signed for
// JEC with the sign-in's nonce, and the person it names must be allowed.
// Then the browser gets a session and goes to the UI; otherwise it goes to
// the sign-in page, with the reason.
func (m *Module) callback(ctx context.Context, in *callbackInput) (*redirectOutput, error) {
	s, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	forget := http.Cookie{ //nolint:gosec // G124: as in login
		Name: stateCookie, Path: cookiePath(s), MaxAge: -1, HttpOnly: true, Secure: secure(s), SameSite: http.SameSiteLaxMode,
	}
	fail := func(reason string) (*redirectOutput, error) {
		return &redirectOutput{Status: http.StatusFound, Location: home(s) + "login?sso=" + reason, SetCookie: []http.Cookie{forget}, CacheCtl: "no-store"}, nil
	}
	if !s.Enabled {
		return fail(failOff)
	}
	if in.State == "" || subtle.ConstantTimeCompare([]byte(in.State), []byte(in.Cookie.Value)) != 1 {
		return fail(failExpired)
	}
	p, ok, err := m.take(ctx, in.State)
	switch {
	case err != nil:
		return nil, err
	case !ok:
		return fail(failExpired)
	case in.Error != "":
		m.log.Info("oidc sign-in refused by the provider", "error", in.Error, "description", in.Description)
		return fail(failDenied)
	}
	prov, err := m.provider(ctx, s)
	if err != nil {
		m.log.Warn("oidc discovery", "issuer", s.Issuer, "err", err)
		return fail(failInvalid)
	}
	raw, err := exchange(ctx, m.client, prov.d, s.ClientID, s.Secret, s.RedirectURI, in.Code, p.Verifier)
	if err != nil {
		m.log.Warn("oidc token exchange", "err", err)
		return fail(failInvalid)
	}
	who, keys, err := verify(ctx, m.client, prov.d, prov.keys, raw, s.ClientID, p.Nonce, m.now())
	m.keep(s, keys)
	if err != nil {
		m.log.Warn("oidc ID token", "err", err)
		return fail(failInvalid)
	}
	if !allowed(s.Allowed, who) {
		m.log.Warn("oidc sign-in of someone not allowed", "sub", who.Subject, "email", who.Email)
		return fail(failNotAllowed)
	}
	session, err := m.startSession(ctx)
	if errors.Is(err, auth.ErrNoAccount) {
		return fail(failNoAccount)
	}
	if err != nil {
		return nil, err
	}
	m.log.Info("signed in through oidc", "sub", who.Subject, "email", who.Email)
	return &redirectOutput{Status: http.StatusFound, Location: home(s), SetCookie: []http.Cookie{forget, session}, CacheCtl: "no-store"}, nil
}

// take returns what the sign-in of state keeps, and forgets it: a state
// works once, and for loginLifetime.
func (m *Module) take(ctx context.Context, state string) (pending, bool, error) {
	var p pending
	var sealed []byte
	var created int64
	err := m.db.QueryRowContext(ctx, "DELETE FROM oidc_logins WHERE state_hash = ? RETURNING sealed, created_at", hashState(state)).Scan(&sealed, &created)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return p, false, nil
	case err != nil:
		return p, false, err
	case m.now().Sub(time.UnixMilli(created)) > loginLifetime:
		return p, false, nil
	}
	data, err := m.secrets.Open(sealed, "oidc login")
	if err != nil {
		return p, false, err
	}
	return p, json.Unmarshal(data, &p) == nil, nil
}

// provider returns the provider of the settings, discovered within the
// last hour.
func (m *Module) provider(ctx context.Context, s settings) (*provider, error) {
	m.mu.Lock()
	p := m.cached
	m.mu.Unlock()
	if p != nil && p.issuer == s.Issuer && m.now().Sub(p.at) < discoveryTTL {
		return p, nil
	}
	d, err := discover(ctx, m.client, s.Issuer)
	if err != nil {
		return nil, err
	}
	p = &provider{issuer: s.Issuer, d: d, at: m.now()}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cached != nil && m.cached.issuer == s.Issuer {
		p.keys = m.cached.keys
	}
	m.cached = p
	return p, nil
}

// keep remembers the provider's keys, as fetched for a token.
func (m *Module) keep(s settings, keys *jose.JSONWebKeySet) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if keys != nil && m.cached != nil && m.cached.issuer == s.Issuer {
		m.cached.keys = keys
	}
}

// allowed tells whether the person who signed in is one of list: by an
// email address the provider verified, or else by subject.
func allowed(list []string, who identity) bool {
	verified := who.Email != "" && (who.EmailVerified == nil || *who.EmailVerified)
	for _, a := range list {
		if strings.Contains(a, "@") {
			if verified && strings.EqualFold(a, who.Email) {
				return true
			}
		} else if a == who.Subject {
			return true
		}
	}
	return false
}

package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

// grant is what the fake provider knows of a code it handed out.
type grant struct {
	challenge, nonce, sub, email string
	verified                     bool
}

// fakeIDP is an OpenID Connect provider: it hands out ID tokens for the
// codes the test approves, checking the client and the PKCE verifier.
type fakeIDP struct {
	t     *testing.T
	srv   *httptest.Server
	key   *ecdsa.PrivateKey
	kid   string
	mu    sync.Mutex
	codes map[string]grant
	// claims, if set, changes what the next ID tokens say; signer, if set,
	// signs them in place of the provider's key.
	claims func(map[string]any)
	signer jose.Signer
}

func newIDP(t *testing.T) *fakeIDP {
	t.Helper()
	p := &fakeIDP{t: t, codes: map[string]grant{}, kid: "k1"}
	p.key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeIDP) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token",
			"jwks_uri": p.srv.URL + "/jwks", "token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
		})
	case "/jwks":
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: p.kid, Algorithm: "ES256", Use: "sig"}}})
	case "/token":
		id, secret, ok := r.BasicAuth()
		_ = r.ParseForm()
		g, known := p.codes[r.PostForm.Get("code")]
		delete(p.codes, r.PostForm.Get("code"))
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		switch {
		case !ok || id != "jec" || secret != "s3cret":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		case !known || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge || r.PostForm.Get("grant_type") != "authorization_code":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": p.token(g)})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// token signs the ID token of g.
func (p *fakeIDP) token(g grant) string {
	now := time.Now()
	claims := map[string]any{
		"iss": p.srv.URL, "aud": "jec", "sub": g.sub, "email": g.email, "email_verified": g.verified, "nonce": g.nonce,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
	}
	if p.claims != nil {
		p.claims(claims)
	}
	signer := p.signer
	if signer == nil {
		var err error
		signer, err = jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: p.key}, (&jose.SignerOptions{}).WithHeader("kid", p.kid))
		if err != nil {
			p.t.Fatal(err)
		}
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		p.t.Fatal(err)
	}
	return raw
}

// fixture is the module with a provider, and a recorder of sessions.
type fixture struct {
	t        *testing.T
	m        *Module
	idp      *fakeIDP
	api      humatest.TestAPI
	sessions int
	noAcct   bool
	now      time.Time
	// authorize is where sign-ins go at the provider.
	authorize string
}

const redirect = "https://jec.example.com/sub/api/v1/oidc/callback"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New()
	if _, err := st.Migrate(t.Context(), "oidc", m.Migrations()); err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, m: m, idp: newIDP(t), now: time.Now()}
	f.authorize = f.idp.srv.URL + "/authorize?"
	m.db, m.secrets, m.log, m.client = st.DB(), box.Scope("oidc"), slog.New(slog.DiscardHandler), f.idp.srv.Client()
	m.now = func() time.Time { return f.now }
	m.startSession = func(context.Context) (http.Cookie, error) {
		if f.noAcct {
			return http.Cookie{}, auth.ErrNoAccount
		}
		f.sessions++
		return http.Cookie{Name: "jec_session", Value: "s1", Path: "/sub/", HttpOnly: true}, nil
	}
	_, f.api = humatest.New(t)
	m.register(f.api)
	return f
}

// settings saves the settings of a working sign-on, changed by change.
func (f *fixture) settings(change func(map[string]any)) *httptest.ResponseRecorder {
	f.t.Helper()
	body := map[string]any{
		"enabled": true, "label": "Auth", "issuer": f.idp.srv.URL, "clientId": "jec", "clientSecret": "s3cret",
		"allowed": []string{"admin@example.com", "u-7"}, "redirectUri": redirect,
	}
	if change != nil {
		change(body)
	}
	return f.api.Put("/settings", body)
}

// login starts a sign-in, and returns its state, the cookie that holds it,
// and the authorization request.
func (f *fixture) login() (state string, cookie *http.Cookie, q url.Values) {
	f.t.Helper()
	resp := f.api.Get("/login")
	if resp.Code != http.StatusFound {
		f.t.Fatalf("login: %d %s", resp.Code, resp.Body)
	}
	u, err := url.Parse(resp.Header().Get("Location"))
	if err != nil || !strings.HasPrefix(u.String(), f.authorize) {
		f.t.Fatalf("login goes to %s", u)
	}
	for _, c := range (&http.Response{Header: resp.Header()}).Cookies() {
		if c.Name == stateCookie {
			cookie = c
		}
	}
	q = u.Query()
	if cookie == nil || cookie.Value != q.Get("state") {
		f.t.Fatalf("state cookie %v for state %q", cookie, q.Get("state"))
	}
	return q.Get("state"), cookie, q
}

// approve has the provider hand out code for the sign-in of q, for who.
func (f *fixture) approve(code string, q url.Values, who grant) {
	who.challenge, who.nonce = q.Get("code_challenge"), q.Get("nonce")
	f.idp.mu.Lock()
	defer f.idp.mu.Unlock()
	f.idp.codes[code] = who
}

// callback returns from the provider, and tells where the browser goes and
// the cookies it gets.
func (f *fixture) callback(query string, cookie *http.Cookie) (string, []*http.Cookie) {
	f.t.Helper()
	var headers []any
	if cookie != nil {
		headers = append(headers, "Cookie: "+stateCookie+"="+cookie.Value)
	}
	resp := f.api.Get("/callback?"+query, headers...)
	if resp.Code != http.StatusFound {
		f.t.Fatalf("callback: %d %s", resp.Code, resp.Body)
	}
	return resp.Header().Get("Location"), (&http.Response{Header: resp.Header()}).Cookies()
}

var admin = grant{sub: "u-1", email: "admin@example.com", verified: true}

func TestSignIn(t *testing.T) {
	f := newFixture(t)
	if resp := f.settings(nil); resp.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", resp.Code, resp.Body)
	}
	var status OIDCStatusBody
	if resp := f.api.Get("/status"); resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &status) != nil || !status.Enabled || status.Label != "Auth" {
		t.Fatalf("status %s", resp.Body)
	}

	state, cookie, q := f.login()
	if q.Get("client_id") != "jec" || q.Get("redirect_uri") != redirect || q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" ||
		!strings.Contains(q.Get("scope"), "openid") || q.Get("response_type") != "code" {
		t.Errorf("authorization request %v", q)
	}
	if cookie.Path != "/sub/api/v1/oidc/" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 600 {
		t.Errorf("state cookie %+v", cookie)
	}
	f.approve("c1", q, admin)
	to, cookies := f.callback("code=c1&state="+state, cookie)
	if to != "https://jec.example.com/sub/" || f.sessions != 1 {
		t.Fatalf("signed in: to %s, %d sessions", to, f.sessions)
	}
	names := map[string]*http.Cookie{}
	for _, c := range cookies {
		names[c.Name] = c
	}
	if names["jec_session"] == nil || names[stateCookie] == nil || names[stateCookie].MaxAge >= 0 {
		t.Errorf("cookies %v", cookies)
	}

	// The same return again: the state is spent.
	f.approve("c2", q, admin)
	if to, _ := f.callback("code=c2&state="+state, cookie); to != "https://jec.example.com/sub/login?sso=expired" || f.sessions != 1 {
		t.Errorf("replayed: %s", to)
	}
}

func TestSignInFailures(t *testing.T) {
	f := newFixture(t)
	if resp := f.settings(nil); resp.Code != http.StatusOK {
		t.Fatal(resp.Body)
	}
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	otherSigner, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: other}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	hmacSigner, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte("s3cret-s3cret-s3cret-s3cret-s3cret")}, nil)
	for name, c := range map[string]struct {
		who    grant
		claims func(map[string]any)
		signer jose.Signer
		query  func(state string) string
		cookie bool // sends another cookie
		later  time.Duration
		noAcct bool
		want   string
	}{
		"another browser":     {who: admin, cookie: true, want: failExpired},
		"too late":            {who: admin, later: 11 * time.Minute, want: failExpired},
		"refused":             {who: admin, query: func(s string) string { return "error=access_denied&state=" + s }, want: failDenied},
		"no such code":        {who: admin, query: func(s string) string { return "code=nope&state=" + s }, want: failInvalid},
		"signed by another":   {who: admin, signer: otherSigner, want: failInvalid},
		"signed with hmac":    {who: admin, signer: hmacSigner, want: failInvalid},
		"for another client":  {who: admin, claims: func(c map[string]any) { c["aud"] = "other" }, want: failInvalid},
		"another nonce":       {who: admin, claims: func(c map[string]any) { c["nonce"] = "x" }, want: failInvalid},
		"expired":             {who: admin, claims: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, want: failInvalid},
		"another issuer":      {who: admin, claims: func(c map[string]any) { c["iss"] = "https://evil.example" }, want: failInvalid},
		"unverified email":    {who: grant{sub: "u-1", email: "admin@example.com"}, want: failNotAllowed},
		"someone else":        {who: grant{sub: "u-2", email: "eve@example.com", verified: true}, want: failNotAllowed},
		"allowed by subject":  {who: grant{sub: "u-7"}, want: ""},
		"no account yet":      {who: admin, noAcct: true, want: failNoAccount},
		"several audiences":   {who: admin, claims: func(c map[string]any) { c["aud"] = []string{"jec", "other"} }, want: failInvalid},
		"several, azp is JEC": {who: admin, claims: func(c map[string]any) { c["aud"], c["azp"] = []string{"jec", "other"}, "jec" }, want: ""},
	} {
		f.idp.claims, f.idp.signer, f.noAcct, f.now = c.claims, c.signer, c.noAcct, time.Now()
		state, cookie, q := f.login()
		f.approve("code-"+name, q, c.who)
		query := "code=" + url.QueryEscape("code-"+name) + "&state=" + state
		if c.query != nil {
			query = c.query(state)
		}
		if c.cookie {
			cookie = &http.Cookie{Value: "another"}
		}
		f.now = f.now.Add(c.later)
		want := "https://jec.example.com/sub/login?sso=" + c.want
		if c.want == "" {
			want = "https://jec.example.com/sub/"
		}
		if to, _ := f.callback(query, cookie); to != want {
			t.Errorf("%s: to %s", name, to)
		}
	}
}

func TestKeysRotate(t *testing.T) {
	f := newFixture(t)
	if resp := f.settings(nil); resp.Code != http.StatusOK {
		t.Fatal(resp.Body)
	}
	for _, kid := range []string{"k1", "k2"} {
		f.idp.mu.Lock()
		f.idp.kid = kid
		f.idp.key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		f.idp.mu.Unlock()
		state, cookie, q := f.login()
		f.approve("c-"+kid, q, admin)
		if to, _ := f.callback("code=c-"+kid+"&state="+state, cookie); to != "https://jec.example.com/sub/" {
			t.Errorf("with key %s: to %s", kid, to)
		}
	}
}

func TestSettings(t *testing.T) {
	f := newFixture(t)
	for name, c := range map[string]struct {
		change func(map[string]any)
		field  string
		key    i18n.Key
	}{
		"plain http on the internet": {func(b map[string]any) { b["issuer"] = "http://auth.example.com" }, "body.issuer", i18n.OidcErrorIssuer},
		"no client":                  {func(b map[string]any) { b["clientId"] = " " }, "body.clientId", i18n.KernelErrorFieldRequired},
		"no one allowed":             {func(b map[string]any) { b["allowed"] = []string{" "} }, "body.allowed", i18n.KernelErrorFieldRequired},
		"another callback":           {func(b map[string]any) { b["redirectUri"] = "https://jec.example.com/callback" }, "body.redirectUri", i18n.OidcErrorRedirect},
		"no provider there":          {func(b map[string]any) { b["issuer"] = f.idp.srv.URL + "/nope" }, "body.issuer", i18n.OidcErrorDiscovery},
	} {
		resp := f.settings(c.change)
		var e struct {
			Fields []struct {
				Location string `json:"location"`
				Code     string `json:"code"`
			} `json:"fields"`
		}
		if resp.Code != http.StatusUnprocessableEntity || json.Unmarshal(resp.Body.Bytes(), &e) != nil || len(e.Fields) == 0 ||
			e.Fields[0].Location != c.field || e.Fields[0].Code != string(c.key) {
			t.Errorf("%s: %d %s", name, resp.Code, resp.Body)
		}
	}

	// Saved, the secret is never shown; left out, it stays; empty, it goes.
	if resp := f.settings(nil); resp.Code != http.StatusOK || strings.Contains(resp.Body.String(), "s3cret") {
		t.Fatalf("saved: %d %s", resp.Code, resp.Body)
	}
	if resp := f.settings(func(b map[string]any) { delete(b, "clientSecret") }); !strings.Contains(resp.Body.String(), `"hasSecret":true`) {
		t.Errorf("kept: %s", resp.Body)
	}
	if resp := f.settings(func(b map[string]any) { b["clientSecret"] = "" }); !strings.Contains(resp.Body.String(), `"hasSecret":false`) {
		t.Errorf("removed: %s", resp.Body)
	}

	// Switched off, the settings stay unchecked, and sign-ins end at once.
	if resp := f.settings(func(b map[string]any) { b["enabled"], b["issuer"] = false, "http://nowhere.example" }); resp.Code != http.StatusOK {
		t.Fatalf("off: %d %s", resp.Code, resp.Body)
	}
	if resp := f.api.Get("/status"); !strings.Contains(resp.Body.String(), `"enabled":false`) {
		t.Errorf("status %s", resp.Body)
	}
	if resp := f.api.Get("/login"); resp.Code != http.StatusFound || resp.Header().Get("Location") != "https://jec.example.com/sub/login?sso=off" {
		t.Errorf("login while off: %d %s", resp.Code, resp.Header().Get("Location"))
	}
}

func TestIssuerOK(t *testing.T) {
	for issuer, want := range map[string]bool{
		"https://auth.example.com":      true,
		"https://auth.example.com/r/my": true,
		"http://localhost:9091":         true,
		"http://127.0.0.1:5556/dex":     true,
		"http://192.168.1.10:9091":      true,
		"http://auth.example.com":       false,
		"http://8.8.8.8":                false,
		"ftp://auth.example.com":        false,
		"https://auth.example.com?x=1":  false,
		"https://u@auth.example.com":    false,
		"auth.example.com":              false,
	} {
		if got := issuerOK(issuer); got != want {
			t.Errorf("%s: %v", issuer, got)
		}
	}
}

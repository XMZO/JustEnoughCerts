package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/clientip"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

// Cheap hashes keep the tests fast; production parameters are tested by
// TestPasswordDefaults.
func TestMain(m *testing.M) {
	argonTime, argonMemory, argonThreads = 1, 64, 1
	os.Exit(m.Run())
}

type fixture struct {
	t     *testing.T
	srv   *httptest.Server
	svc   *Service
	token string
	clock time.Time
}

func newFixture(t *testing.T, opts Options) *fixture {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	if opts.Secrets == nil {
		dir := t.TempDir()
		box, _, err := secrets.Load(dir, "")
		if err != nil {
			t.Fatal(err)
		}
		opts.Secrets = box.Scope("kernel")
	}
	svc, err := New(t.Context(), st.DB(), slog.New(slog.DiscardHandler), opts)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, svc: svc, token: svc.setupToken, clock: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	svc.now = func() time.Time { return f.clock }

	set, err := module.NewSet()
	if err != nil {
		t.Fatal(err)
	}
	a, err := api.New(api.Options{Version: "test", Middlewares: []func(huma.Context, func(huma.Context)){svc.Middleware}, SessionCookie: CookieName},
		set, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	svc.Register(a.Kernel())
	huma.Register(a.Kernel(), huma.Operation{OperationID: "whoami", Method: http.MethodGet, Path: "/whoami"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body string }, error) {
			u, ok := UserFrom(ctx)
			if !ok {
				t.Error("a protected operation ran without a user")
			}
			return &struct{ Body string }{Body: u.Username}, nil
		})
	huma.Register(a.Kernel(), huma.Operation{OperationID: "confirm", Method: http.MethodPost, Path: "/confirm", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			Body struct {
				Password string `json:"password"`
			}
		}) (*struct{}, error) {
			return nil, svc.ConfirmPassword(ctx, in.Body.Password)
		})
	f.srv = httptest.NewServer(a.Handler())
	t.Cleanup(f.srv.Close)
	return f
}

type result struct {
	status int
	header http.Header
	body   string
	cookie *http.Cookie
}

func (f *fixture) call(method, path, cookie, body string) result {
	f.t.Helper()
	req, err := http.NewRequestWithContext(f.t.Context(), method, f.srv.URL+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	r := result{status: resp.StatusCode, header: resp.Header, body: string(data)}
	for _, c := range resp.Cookies() {
		if c.Name == CookieName {
			r.cookie = c
		}
	}
	return r
}

func code(t *testing.T, body string) i18n.Key {
	t.Helper()
	var e struct {
		Code   i18n.Key       `json:"code"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("not an error: %s", body)
	}
	return e.Code
}

func (f *fixture) setupBody(token string) string {
	return `{"token": "` + token + `", "username": "admin", "password": "correct horse", "locale": "zh-cn"}`
}

func TestSetup(t *testing.T) {
	f := newFixture(t, Options{CookiePath: "/jec", Secure: true})
	if f.token == "" {
		t.Fatal("no setup token without an account")
	}
	if r := f.call(http.MethodGet, "/setup", "", ""); r.body != "{\"required\":true}\n" {
		t.Errorf("GET /setup = %s", r.body)
	}
	if r := f.call(http.MethodPost, "/setup", "", f.setupBody("wrong")); r.status != http.StatusForbidden || code(t, r.body) != i18n.KernelErrorSetupTokenInvalid {
		t.Errorf("wrong token = %d %s", r.status, r.body)
	}
	short := `{"token": "` + f.token + `", "username": "admin", "password": "short"}`
	if r := f.call(http.MethodPost, "/setup", "", short); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "body.password") {
		t.Errorf("short password = %d %s", r.status, r.body)
	}
	badLocale := `{"token": "` + f.token + `", "username": "admin", "password": "correct horse", "locale": "not a tag!"}`
	if r := f.call(http.MethodPost, "/setup", "", badLocale); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "body.locale") {
		t.Errorf("bad locale = %d %s", r.status, r.body)
	}

	// The token may come with stray spaces and capitals from a copy.
	r := f.call(http.MethodPost, "/setup", "", f.setupBody("  "+strings.ToUpper(f.token)+" "))
	if r.status != http.StatusCreated || r.body != "{\"username\":\"admin\",\"locale\":\"zh-CN\",\"passwordChangedAt\":\"2026-10-16T12:00:00Z\",\"twoFactor\":false,\"sessionDays\":7}\n" {
		t.Fatalf("setup = %d %s", r.status, r.body)
	}
	c := r.cookie
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/jec" || c.MaxAge != int((defaultSessionDays*24*time.Hour).Seconds()) {
		t.Errorf("session cookie = %+v", c)
	}
	if r := f.call(http.MethodGet, "/session", c.Value, ""); r.status != http.StatusOK || !strings.Contains(r.body, `"username":"admin"`) {
		t.Errorf("GET /session = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodGet, "/setup", "", ""); r.body != "{\"required\":false}\n" {
		t.Errorf("GET /setup after setup = %s", r.body)
	}
	if r := f.call(http.MethodPost, "/setup", "", f.setupBody(f.token)); r.status != http.StatusConflict || code(t, r.body) != i18n.KernelErrorSetupDone {
		t.Errorf("second setup = %d %s", r.status, r.body)
	}
}

func TestConcurrentSetup(t *testing.T) {
	f := newFixture(t, Options{CookiePath: "/"})
	var wg sync.WaitGroup
	statuses := make([]int, 8)
	for i := range statuses {
		wg.Go(func() {
			body := `{"token": "` + f.token + `", "username": "admin` + string(rune('a'+i)) + `", "password": "correct horse"}`
			statuses[i] = f.call(http.MethodPost, "/setup", "", body).status
		})
	}
	wg.Wait()
	created := 0
	for _, s := range statuses {
		switch s {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Errorf("unexpected status %d", s)
		}
	}
	if created != 1 {
		t.Errorf("%d accounts created, want 1: %v", created, statuses)
	}
}

func signedUp(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, Options{CookiePath: "/"})
	if r := f.call(http.MethodPost, "/setup", "", f.setupBody(f.token)); r.status != http.StatusCreated {
		t.Fatalf("setup = %d %s", r.status, r.body)
	}
	return f
}

func TestSignInAndOut(t *testing.T) {
	f := signedUp(t)
	for _, body := range []string{
		`{"username": "admin", "password": "wrong horse"}`,
		`{"username": "nobody", "password": "correct horse"}`,
	} {
		if r := f.call(http.MethodPost, "/session", "", body); r.status != http.StatusUnauthorized || code(t, r.body) != i18n.KernelErrorLoginFailed || r.cookie != nil {
			t.Errorf("sign in with %s = %d %s", body, r.status, r.body)
		}
	}
	r := f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "correct horse"}`)
	if r.status != http.StatusOK || r.cookie == nil || r.cookie.Secure {
		t.Fatalf("sign in = %d %s %+v", r.status, r.body, r.cookie)
	}
	session := r.cookie.Value
	if r := f.call(http.MethodGet, "/whoami", session, ""); r.body != "\"admin\"\n" {
		t.Errorf("whoami = %d %s", r.status, r.body)
	}
	out := f.call(http.MethodDelete, "/session", session, "")
	if out.status != http.StatusNoContent || out.cookie == nil || out.cookie.MaxAge >= 0 || out.cookie.Value != "" {
		t.Errorf("sign out = %d %+v", out.status, out.cookie)
	}
	if r := f.call(http.MethodGet, "/whoami", session, ""); r.status != http.StatusUnauthorized || code(t, r.body) != i18n.KernelErrorUnauthorized {
		t.Errorf("after sign out = %d %s", r.status, r.body)
	}
}

func TestProtectedByDefault(t *testing.T) {
	f := signedUp(t)
	for _, c := range []string{"", "not-a-session"} {
		if r := f.call(http.MethodGet, "/whoami", c, ""); r.status != http.StatusUnauthorized {
			t.Errorf("whoami with cookie %q = %d", c, r.status)
		}
		if r := f.call(http.MethodGet, "/manifest", c, ""); r.status != http.StatusUnauthorized {
			t.Errorf("manifest with cookie %q = %d", c, r.status)
		}
	}
	r := f.call(http.MethodGet, "/openapi.json", "", "")
	var doc struct {
		Components struct {
			SecuritySchemes map[string]struct{ Type, In, Name string } `json:"securitySchemes"`
		} `json:"components"`
		Paths map[string]map[string]struct {
			Security *[]map[string][]string `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(r.body), &doc); err != nil || r.status != http.StatusOK {
		t.Fatalf("openapi.json = %d, %v", r.status, err)
	}
	if s := doc.Components.SecuritySchemes[api.SessionScheme]; s.Type != "apiKey" || s.In != "cookie" || s.Name != CookieName {
		t.Errorf("security scheme = %+v", s)
	}
	// Public operations say so; the rest inherit the global requirement.
	if sec := doc.Paths["/session"]["post"].Security; sec == nil || len(*sec) != 0 {
		t.Errorf("POST /session security = %v, want []", sec)
	}
	if sec := doc.Paths["/session"]["get"].Security; sec != nil {
		t.Errorf("GET /session security = %v, want the global one", *sec)
	}
}

func TestSessionExpires(t *testing.T) {
	f := signedUp(t)
	session := f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "correct horse"}`).cookie.Value
	f.clock = f.clock.Add(defaultSessionDays*24*time.Hour - time.Minute)
	if r := f.call(http.MethodGet, "/whoami", session, ""); r.status != http.StatusOK {
		t.Errorf("before expiry = %d", r.status)
	}
	f.clock = f.clock.Add(2 * time.Minute)
	if r := f.call(http.MethodGet, "/whoami", session, ""); r.status != http.StatusUnauthorized {
		t.Errorf("after expiry = %d", r.status)
	}
}

func TestThrottle(t *testing.T) {
	f := signedUp(t)
	wrong := `{"username": "admin", "password": "wrong horse"}`
	for i := range maxFailures {
		// Each failure says what too many of them lead to.
		r := f.call(http.MethodPost, "/session", "", wrong)
		if r.status != http.StatusUnauthorized || !strings.Contains(r.body, `"attempts":5`) || !strings.Contains(r.body, `"minutes":15`) {
			t.Fatalf("failure %d = %d %s", i+1, r.status, r.body)
		}
	}
	right := `{"username": "admin", "password": "correct horse"}`
	r := f.call(http.MethodPost, "/session", "", right)
	if r.status != http.StatusTooManyRequests || code(t, r.body) != i18n.KernelErrorTooManyAttempts ||
		!strings.Contains(r.body, `"minutes":15`) || r.header.Get("Retry-After") != "900" {
		t.Fatalf("after %d failures = %d %s Retry-After %q", maxFailures, r.status, r.body, r.header.Get("Retry-After"))
	}
	f.clock = f.clock.Add(lockout)
	if r := f.call(http.MethodPost, "/session", "", right); r.status != http.StatusOK {
		t.Errorf("after the lockout = %d %s", r.status, r.body)
	}
}

// TestThrottleBehindProxy locks out the client a trusted proxy forwards
// for, not the proxy: another client signs in still.
func TestThrottleBehindProxy(t *testing.T) {
	clients, err := clientip.New("127.0.0.1, ::1")
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, Options{CookiePath: "/", Clients: clients})
	if r := f.call(http.MethodPost, "/setup", "", f.setupBody(f.token)); r.status != http.StatusCreated {
		t.Fatalf("setup = %d %s", r.status, r.body)
	}
	signIn := func(client, password string) int {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.srv.URL+"/session",
			strings.NewReader(`{"username": "admin", "password": "`+password+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", client)
		resp, err := f.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for range maxFailures {
		signIn("198.51.100.1", "wrong horse")
	}
	if got := signIn("198.51.100.1", "correct horse"); got != http.StatusTooManyRequests {
		t.Errorf("the client that failed: %d", got)
	}
	if got := signIn("198.51.100.2", "correct horse"); got != http.StatusOK {
		t.Errorf("another client: %d", got)
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(2, time.Minute)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return now.Add(time.Duration(s) * time.Second) }
	if l.wait("a", now) != 0 {
		t.Fatal("a fresh client must wait")
	}
	l.fail("a", at(0))
	if l.wait("a", at(0)) != 0 {
		t.Error("locked out after one failure")
	}
	l.fail("a", at(10))
	if got := l.wait("a", at(20)); got != 50*time.Second {
		t.Errorf("wait = %v, want 50s", got)
	}
	if l.wait("b", at(20)) != 0 {
		t.Error("another client is locked out too")
	}
	if l.wait("a", at(70)) != 0 {
		t.Error("the lockout outlasted its minute")
	}

	// A success ends a streak, and so does a pause as long as a lockout.
	l.reset("a")
	l.fail("a", at(100))
	l.reset("a")
	l.fail("a", at(101))
	if l.wait("a", at(101)) != 0 {
		t.Error("failures on both sides of a success were added up")
	}
	l.fail("a", at(161))
	if l.wait("a", at(161)) != 0 {
		t.Error("failures a lockout apart were added up")
	}
}

func TestSetPassword(t *testing.T) {
	f := signedUp(t)
	ctx := t.Context()
	db := f.svc.db
	session := f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "correct horse"}`).cookie.Value
	if err := SetPassword(ctx, db, "admin", "short"); err == nil {
		t.Error("a short password was accepted")
	}
	if err := SetPassword(ctx, db, "nobody", "a long password"); !errors.Is(err, ErrNoAccount) {
		t.Errorf("unknown account: %v", err)
	}
	if err := SetPassword(ctx, db, "admin", "brand new password"); err != nil {
		t.Fatal(err)
	}
	if r := f.call(http.MethodGet, "/whoami", session, ""); r.status != http.StatusUnauthorized {
		t.Errorf("an old session survived the new password: %d", r.status)
	}
	if r := f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "correct horse"}`); r.status != http.StatusUnauthorized {
		t.Errorf("the old password still works: %d", r.status)
	}
	if r := f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "brand new password"}`); r.status != http.StatusOK {
		t.Errorf("the new password fails: %d %s", r.status, r.body)
	}
	if names, err := Usernames(ctx, db); err != nil || len(names) != 1 || names[0] != "admin" {
		t.Errorf("Usernames = %v, %v", names, err)
	}
}

func TestPassword(t *testing.T) {
	hash, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := checkPassword(hash, "correct horse"); !ok || err != nil {
		t.Errorf("right password: %v, %v", ok, err)
	}
	if ok, err := checkPassword(hash, "wrong horse"); ok || err != nil {
		t.Errorf("wrong password: %v, %v", ok, err)
	}
	other, _ := hashPassword("correct horse")
	if other == hash {
		t.Error("two hashes of one password are equal: no salt")
	}
	parts := strings.Split(hash, "$")
	for _, bad := range []string{
		"",
		"plain",
		strings.Replace(hash, "argon2id", "argon2i", 1),
		strings.Replace(hash, "v=19", "v=16", 1),
		strings.Join(append(append([]string{}, parts[:3]...), "m=4194304,t=3,p=4", parts[4], parts[5]), "$"), // 4 GiB
		strings.Join(append(append([]string{}, parts[:5]...), "c2hvcnQ"), "$"),                               // 5-byte key
		strings.Join(append(append([]string{}, parts[:4]...), "!!", parts[5]), "$"),
	} {
		if ok, err := checkPassword(bad, "correct horse"); ok || err == nil {
			t.Errorf("checkPassword(%q) = %v, %v; want an error", bad, ok, err)
		}
	}
}

func TestPasswordDefaults(t *testing.T) {
	saved := []any{argonTime, argonMemory, argonThreads}
	argonTime, argonMemory, argonThreads = 3, 64*1024, 4
	defer func() {
		argonTime, argonMemory, argonThreads = saved[0].(uint32), saved[1].(uint32), saved[2].(uint8)
	}()
	hash, err := hashPassword("x")
	if err != nil || !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Errorf("hash = %q, %v", hash, err)
	}
}

func TestConfirmPassword(t *testing.T) {
	f := signedUp(t)
	r := f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "correct horse"}`)
	session := r.cookie.Value

	if r := f.call(http.MethodPost, "/confirm", session, `{"password": "correct horse"}`); r.status != http.StatusNoContent {
		t.Errorf("the right password = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodPost, "/confirm", "", `{"password": "correct horse"}`); r.status != http.StatusUnauthorized {
		t.Errorf("without a session = %d %s", r.status, r.body)
	}
	for i := range maxFailures {
		r := f.call(http.MethodPost, "/confirm", session, `{"password": "wrong horse"}`)
		if r.status != http.StatusUnprocessableEntity || code(t, r.body) != i18n.KernelErrorPasswordWrong || !strings.Contains(r.body, `"location":"body.password"`) {
			t.Fatalf("wrong password %d = %d %s", i+1, r.status, r.body)
		}
	}
	// Wrong confirmations lock the client out like failed sign-ins.
	if r := f.call(http.MethodPost, "/confirm", session, `{"password": "correct horse"}`); r.status != http.StatusTooManyRequests {
		t.Errorf("after %d wrong passwords = %d %s", maxFailures, r.status, r.body)
	}
	if r := f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "correct horse"}`); r.status != http.StatusTooManyRequests {
		t.Errorf("sign in after the lockout started = %d %s", r.status, r.body)
	}
}

func TestChangePassword(t *testing.T) {
	f := signedUp(t)
	signIn := func(password string) result {
		return f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "`+password+`"}`)
	}
	here, elsewhere := signIn("correct horse").cookie.Value, signIn("correct horse").cookie.Value

	r := f.call(http.MethodPut, "/account/password", here, `{"password": "wrong horse", "newPassword": "brand new password"}`)
	if r.status != http.StatusUnprocessableEntity || code(t, r.body) != i18n.KernelErrorPasswordWrong || !strings.Contains(r.body, `"location":"body.password"`) {
		t.Errorf("a wrong current password = %d %s", r.status, r.body)
	}
	r = f.call(http.MethodPut, "/account/password", here, `{"password": "correct horse", "newPassword": "short"}`)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "body.newPassword") {
		t.Errorf("a short new password = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodPut, "/account/password", "", `{"password": "correct horse", "newPassword": "brand new password"}`); r.status != http.StatusUnauthorized {
		t.Errorf("signed out = %d %s", r.status, r.body)
	}

	f.clock = f.clock.Add(time.Hour)
	if r := f.call(http.MethodPut, "/account/password", here, `{"password": "correct horse", "newPassword": "brand new password"}`); r.status != http.StatusNoContent {
		t.Fatalf("change = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodGet, "/whoami", here, ""); r.status != http.StatusOK {
		t.Errorf("the session that changed it ended: %d", r.status)
	}
	if r := f.call(http.MethodGet, "/whoami", elsewhere, ""); r.status != http.StatusUnauthorized {
		t.Errorf("another session survived: %d", r.status)
	}
	if r := signIn("correct horse"); r.status != http.StatusUnauthorized {
		t.Errorf("the old password still works: %d", r.status)
	}
	if r := signIn("brand new password"); r.status != http.StatusOK {
		t.Errorf("the new password fails: %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodGet, "/session", here, ""); !strings.Contains(r.body, `"passwordChangedAt":"2026-10-16T13:00:00Z"`) {
		t.Errorf("GET /session = %s", r.body)
	}
}

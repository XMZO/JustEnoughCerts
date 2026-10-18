package oidc

import (
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestLive signs in through a real provider when JEC_TEST_OIDC is its
// issuer, as Dex in a throwaway container, with a client
// (JEC_TEST_OIDC_CLIENT, JEC_TEST_OIDC_SECRET) that may send people back
// to the fixture's callback, and a user (JEC_TEST_OIDC_USER, an email
// address, and JEC_TEST_OIDC_PASSWORD). The test plays the browser on the
// provider's sign-in form.
func TestLive(t *testing.T) {
	issuer := os.Getenv("JEC_TEST_OIDC")
	if issuer == "" {
		t.Skip("JEC_TEST_OIDC is not set")
	}
	user, password := os.Getenv("JEC_TEST_OIDC_USER"), os.Getenv("JEC_TEST_OIDC_PASSWORD")
	f := newFixture(t)
	f.m.client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp := f.settings(func(b map[string]any) {
		b["issuer"], b["clientId"], b["clientSecret"] = issuer, os.Getenv("JEC_TEST_OIDC_CLIENT"), os.Getenv("JEC_TEST_OIDC_SECRET")
		b["allowed"], b["redirectUri"] = []string{user}, "https://jec.example.com/api/v1/oidc/callback"
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", resp.Code, resp.Body)
	}
	p, err := discover(t.Context(), f.m.client, issuer)
	if err != nil {
		t.Fatal(err)
	}
	f.authorize = p.AuthorizationEndpoint + "?"
	state, cookie, q := f.login()
	code := browse(t, f.authorize+q.Encode(), user, password, "https://jec.example.com/api/v1/oidc/callback")
	if to, cookies := f.callback("code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), cookie); to != "https://jec.example.com/" || f.sessions != 1 {
		t.Fatalf("signed in: to %s, cookies %v", to, cookies)
	}
}

// form matches the provider's sign-in form, and its action.
var form = regexp.MustCompile(`(?is)<form[^>]*method="post"[^>]*action="([^"]+)"`)

// browse goes to the provider as a browser does, signs in on its form as
// user, and returns the code it sends the browser back to redirect with.
func browse(t *testing.T, authURL, user, password, redirect string) string {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if strings.HasPrefix(req.URL.String(), redirect) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	resp, err := browser.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	m := form.FindSubmatch(page)
	if m == nil {
		t.Fatalf("no sign-in form at %s:\n%s", resp.Request.URL, page)
	}
	action, err := resp.Request.URL.Parse(html.UnescapeString(string(m[1])))
	if err != nil {
		t.Fatal(err)
	}
	resp, err = browser.PostForm(action.String(), url.Values{"login": {user}, "password": {password}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	back, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode/100 != 3 || err != nil || !strings.HasPrefix(back.String(), redirect) || back.Query().Get("code") == "" {
		t.Fatalf("after signing in: %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	return back.Query().Get("code")
}

package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

// withToken sends a request with an Authorization header and no cookie.
func (f *fixture) withToken(method, path, authorization, body string) result {
	f.t.Helper()
	req, err := http.NewRequestWithContext(f.t.Context(), method, f.srv.URL+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", authorization)
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return result{status: resp.StatusCode, header: resp.Header, body: string(data)}
}

// newToken makes an API token of scope through the session cookie.
func (f *fixture) newToken(cookie, name, scope string) string {
	f.t.Helper()
	r := f.call(http.MethodPost, "/account/tokens", cookie, `{"name": "`+name+`", "scope": "`+scope+`", "password": "correct horse"}`)
	var out struct {
		Token  string    `json:"token"`
		Detail TokenBody `json:"detail"`
	}
	if r.status != http.StatusCreated || json.Unmarshal([]byte(r.body), &out) != nil || !strings.HasPrefix(out.Token, tokenPrefix) ||
		out.Detail.Name != name || out.Detail.Scope != scope {
		f.t.Fatalf("create %s token = %d %s", scope, r.status, r.body)
	}
	return out.Token
}

func TestTokens(t *testing.T) {
	f := signedUp(t)
	r := f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "correct horse"}`)
	cookie := r.cookie.Value

	// Making one takes the password.
	if r := f.call(http.MethodPost, "/account/tokens", cookie, `{"name": "ci", "scope": "write", "password": "wrong horse"}`); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("wrong password = %d %s", r.status, r.body)
	}
	write := f.newToken(cookie, "ci", "write")
	read := f.newToken(cookie, "monitor", "read")

	// A token is the account, without a cookie.
	if r := f.withToken(http.MethodGet, "/whoami", "Bearer "+read, ""); r.status != http.StatusOK || r.body != `"admin"`+"\n" {
		t.Errorf("whoami with a read token = %d %s", r.status, r.body)
	}
	if r := f.withToken(http.MethodPost, "/confirm", "bearer "+write, `{"password": "correct horse"}`); r.status != http.StatusNoContent {
		t.Errorf("a write token's POST = %d %s", r.status, r.body)
	}
	// A read token reads only; no token manages the account.
	if r := f.withToken(http.MethodPost, "/confirm", "Bearer "+read, `{"password": "correct horse"}`); r.status != http.StatusForbidden || code(t, r.body) != i18n.KernelErrorTokenReadOnly {
		t.Errorf("a read token's POST = %d %s", r.status, r.body)
	}
	for _, op := range []struct{ method, path, body string }{
		{http.MethodPut, "/account/password", `{"password": "correct horse", "newPassword": "battery staple"}`},
		{http.MethodGet, "/account/tokens", ""},
		{http.MethodPost, "/account/two-factor/disable", `{"password": "correct horse"}`},
	} {
		if r := f.withToken(op.method, op.path, "Bearer "+write, op.body); r.status != http.StatusForbidden || code(t, r.body) != i18n.KernelErrorTokenSessionOnly {
			t.Errorf("%s %s with a token = %d %s", op.method, op.path, r.status, r.body)
		}
	}
	// A token JEC does not know is refused; another kind of bearer token,
	// as a proxy may pass on, is none of JEC's business.
	if r := f.withToken(http.MethodGet, "/whoami", "Bearer "+tokenPrefix+"nope", ""); r.status != http.StatusUnauthorized || code(t, r.body) != i18n.KernelErrorTokenInvalid {
		t.Errorf("an unknown token = %d %s", r.status, r.body)
	}
	if r := f.withToken(http.MethodGet, "/whoami", "Bearer eyJhbGciOi", ""); r.status != http.StatusUnauthorized || code(t, r.body) != i18n.KernelErrorUnauthorized {
		t.Errorf("a foreign bearer token = %d %s", r.status, r.body)
	}

	// The list tells of them, never their values, with their last use.
	r = f.call(http.MethodGet, "/account/tokens", cookie, "")
	var list struct {
		Tokens []TokenBody `json:"tokens"`
	}
	if err := json.Unmarshal([]byte(r.body), &list); err != nil || len(list.Tokens) != 2 || list.Tokens[0].Name != "monitor" ||
		list.Tokens[0].LastUsedAt == nil || strings.Contains(r.body, tokenPrefix) {
		t.Fatalf("list = %d %s", r.status, r.body)
	}

	// Deleted, a token stops working at once.
	if r := f.call(http.MethodDelete, "/account/tokens/"+strconv.FormatInt(list.Tokens[1].ID, 10), cookie, ""); r.status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", r.status, r.body)
	}
	if r := f.withToken(http.MethodGet, "/whoami", "Bearer "+write, ""); r.status != http.StatusUnauthorized {
		t.Errorf("a deleted token = %d", r.status)
	}
	if r := f.call(http.MethodDelete, "/account/tokens/"+strconv.FormatInt(list.Tokens[1].ID, 10), cookie, ""); r.status != http.StatusNotFound {
		t.Errorf("deleted twice = %d", r.status)
	}

	// Taking the account back on the server deletes them all.
	f.clock = f.clock.Add(time.Hour)
	if err := SetPassword(t.Context(), f.svc.db, "admin", "battery staple"); err != nil {
		t.Fatal(err)
	}
	if r := f.withToken(http.MethodGet, "/whoami", "Bearer "+read, ""); r.status != http.StatusUnauthorized {
		t.Errorf("a token after reset-password = %d", r.status)
	}
}

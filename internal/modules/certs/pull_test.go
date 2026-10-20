package certs

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestPull(t *testing.T) {
	f := newFixture(t)
	u := f.leaf(90, "example.com")
	c := f.add(u)
	path := "/certificates/" + strconv.FormatInt(c.ID, 10) + "/pull"
	var state PullBody
	f.call(f.api.Get(path), http.StatusOK, &state)
	if state.Enabled {
		t.Fatalf("on before a key: %+v", state)
	}
	if resp := f.api.Post(path, map[string]any{"password": "wrong"}); resp.Code == http.StatusOK {
		t.Fatalf("a key without the password = %d", resp.Code)
	}
	var made PullBody
	f.call(f.api.Post(path, map[string]any{"password": "correct horse"}), http.StatusOK, &made)
	if !made.Enabled || !strings.HasPrefix(made.Key, pullPrefix) {
		t.Fatalf("made %+v", made)
	}
	fetch := func(file, key, etag string) (int, string, string) {
		headers := []any{"Authorization: Bearer " + key}
		if etag != "" {
			headers = append(headers, "If-None-Match: "+etag)
		}
		resp := f.api.Get("/pull/"+file, headers...)
		return resp.Code, resp.Body.String(), resp.Header().Get("ETag")
	}
	code, body, etag := fetch("fullchain.pem", made.Key, "")
	if code != http.StatusOK || body != u.cert || etag != `"`+c.Current.Fingerprint+`"` {
		t.Fatalf("fullchain = %d %q %s", code, body, etag)
	}
	if code, body, _ := fetch("privkey.pem", made.Key, ""); code != http.StatusOK || !strings.Contains(body, "PRIVATE KEY") {
		t.Errorf("privkey = %d %.40s", code, body)
	}
	if code, _, _ := fetch("fullchain.pem", made.Key, etag); code != http.StatusNotModified {
		t.Errorf("unchanged = %d", code)
	}
	f.call(f.api.Get(path), http.StatusOK, &state)
	if !state.Enabled || state.UsedAt == nil || state.Key != "" {
		t.Errorf("state %+v", state)
	}

	// A new key ends the old; off, none works.
	var again PullBody
	f.call(f.api.Post(path, map[string]any{"password": "correct horse"}), http.StatusOK, &again)
	for _, c := range []struct {
		key  string
		want int
	}{{made.Key, http.StatusNotFound}, {again.Key, http.StatusOK}, {"jec_" + again.Key[len(pullPrefix):], http.StatusNotFound}} {
		if code, _, _ := fetch("cert.pem", c.key, ""); code != c.want {
			t.Errorf("fetch with %.12s = %d, want %d", c.key, code, c.want)
		}
	}
	f.call(f.api.Delete(path), http.StatusNoContent, nil)
	if code, _, _ := fetch("cert.pem", again.Key, ""); code != http.StatusNotFound {
		t.Errorf("after off = %d", code)
	}
}

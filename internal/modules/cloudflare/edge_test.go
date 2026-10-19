package cloudflare

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// fakeEdge is Cloudflare's API for answering at the edge, and the edge
// itself, which serves Workers KV through JEC's Worker on the routed hosts.
type fakeEdge struct {
	mu      sync.Mutex
	zones   map[string]string // name to ID, all of account "acc"
	saas    bool              // the zone takes no route of a host of its own name
	kv      map[string][]byte // the values of the namespace
	ns      string            // the namespace's ID, once made
	binding string            // the namespace the Worker is bound to
	scripts int
	routes  []map[string]string
	rules   []map[string]any // of the entrypoint, nil until there is one
	noWAF   bool             // the token may not edit the zone's rules
	noPhase bool             // the plan does not let a rule skip phases
}

func (f *fakeEdge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	token := r.Header.Get("Authorization") == "Bearer t0ken"
	globalKey := r.Header.Get("Authorization") == "" && r.Header.Get("X-Auth-Email") == "me@example.com" && r.Header.Get("X-Auth-Key") == "k3y"
	if !token && !globalKey {
		answer(w, http.StatusForbidden, nil, 10000)
		return
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/zones":
		var found []map[string]any
		if id, ok := f.zones[r.URL.Query().Get("name")]; ok {
			found = append(found, map[string]any{"id": id, "name": r.URL.Query().Get("name"), "account": map[string]string{"id": "acc"}})
		}
		answer(w, http.StatusOK, found, 0)
	case r.Method == http.MethodGet && path == "/accounts/acc/storage/kv/namespaces":
		var list []map[string]string
		if f.ns != "" {
			list = append(list, map[string]string{"id": f.ns, "title": kvTitle})
		}
		answer(w, http.StatusOK, list, 0)
	case r.Method == http.MethodPost && path == "/accounts/acc/storage/kv/namespaces":
		f.ns = "ns1"
		answer(w, http.StatusOK, map[string]string{"id": f.ns, "title": kvTitle}, 0)
	case r.Method == http.MethodPut && path == "/accounts/acc/workers/scripts/"+scriptName:
		f.scripts++
		f.binding = workerBinding(r)
		answer(w, http.StatusOK, map[string]string{"id": scriptName}, 0)
	case strings.HasPrefix(path, "/accounts/acc/storage/kv/namespaces/ns1/values/"):
		key, _ := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/accounts/acc/storage/kv/namespaces/ns1/values/"))
		if r.Method == http.MethodDelete {
			delete(f.kv, key)
		} else {
			data, _ := io.ReadAll(r.Body)
			f.kv[key] = data
		}
		answer(w, http.StatusOK, nil, 0)
	case strings.HasSuffix(path, "/workers/routes"):
		if r.Method == http.MethodGet {
			answer(w, http.StatusOK, f.routes, 0)
			return
		}
		var route map[string]string
		_ = json.NewDecoder(r.Body).Decode(&route)
		if f.saas && !strings.HasPrefix(route["pattern"], "*/") {
			answer(w, http.StatusBadRequest, nil, 10022) // not a host of the zone
			return
		}
		f.routes = append(f.routes, route)
		answer(w, http.StatusOK, map[string]string{"id": "r1"}, 0)
	case strings.Contains(path, "/rulesets"):
		f.ruleset(w, r)
	default:
		answer(w, http.StatusNotFound, nil, 7003)
	}
}

func (f *fakeEdge) ruleset(w http.ResponseWriter, r *http.Request) {
	if f.noWAF {
		answer(w, http.StatusForbidden, nil, 10000)
		return
	}
	var rule map[string]any
	if r.Method != http.MethodGet {
		_ = json.NewDecoder(r.Body).Decode(&rule)
	}
	refuse := func(rule map[string]any) bool {
		params, _ := rule["action_parameters"].(map[string]any)
		return f.noPhase && params["phases"] != nil
	}
	switch {
	case r.Method == http.MethodGet && f.rules == nil:
		answer(w, http.StatusNotFound, nil, 10003)
	case r.Method == http.MethodGet:
		answer(w, http.StatusOK, map[string]any{"id": "rs1", "rules": f.rules}, 0)
	case r.Method == http.MethodPut: // a new entrypoint, of the rules given
		rules, _ := rule["rules"].([]any)
		if refuse(rules[0].(map[string]any)) {
			answer(w, http.StatusBadRequest, nil, 20120)
			return
		}
		rules[0].(map[string]any)["id"] = "jec"
		f.rules = []map[string]any{rules[0].(map[string]any)}
		answer(w, http.StatusOK, nil, 0)
	case refuse(rule):
		answer(w, http.StatusBadRequest, nil, 20120)
	case r.Method == http.MethodPost:
		rule["id"] = "jec"
		f.rules = slices.Insert(f.rules, 0, rule)
		answer(w, http.StatusOK, nil, 0)
	case r.Method == http.MethodPatch:
		rule["id"] = "jec"
		f.rules[slices.IndexFunc(f.rules, func(m map[string]any) bool { return m["id"] == "jec" })] = rule
		answer(w, http.StatusOK, nil, 0)
	}
}

// workerBinding returns the namespace the Worker uploaded is bound to, if
// it came with its code as a module.
func workerBinding(r *http.Request) string {
	_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	reader := multipart.NewReader(r.Body, params["boundary"])
	var binding string
	code := false
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(part)
		switch part.FormName() {
		case "metadata":
			var m struct {
				MainModule string `json:"main_module"`
				Bindings   []struct {
					Type        string `json:"type"`
					Name        string `json:"name"`
					NamespaceID string `json:"namespace_id"`
				} `json:"bindings"`
			}
			_ = json.Unmarshal(data, &m)
			if m.MainModule == "worker.js" && len(m.Bindings) == 1 && m.Bindings[0].Type == "kv_namespace" && m.Bindings[0].Name == "FILES" {
				binding = m.Bindings[0].NamespaceID
			}
		case "worker.js":
			code = bytes.Contains(data, []byte("env.FILES.get")) && part.Header.Get("Content-Type") == "application/javascript+module"
		}
	}
	if !code {
		return ""
	}
	return binding
}

// RoundTrip is the edge: a host whose route reaches the Worker answers
// with what Workers KV holds for it.
func (f *fakeEdge) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	routed := slices.ContainsFunc(f.routes, func(route map[string]string) bool {
		return route["pattern"] == "*/.well-known/*" || route["pattern"] == r.URL.Host+"/.well-known/*"
	})
	value, ok := f.kv[r.URL.Host+r.URL.Path]
	if !routed || !ok || f.binding != f.ns {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found")), Request: r}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(value)), Request: r}, nil
}

func answer(w http.ResponseWriter, status int, result any, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"success": status < 300, "result": result, "errors": []any{}}
	if code != 0 {
		body["errors"] = []map[string]any{{"code": code, "message": "refused"}}
	}
	_ = json.NewEncoder(w).Encode(body)
}

func newEdge(t *testing.T, f *fakeEdge, config map[string]string) *edge {
	t.Helper()
	return newEdgeSigned(t, f, config, map[string]string{"api_token": "t0ken"})
}

func newEdgeSigned(t *testing.T, f *fakeEdge, config, secrets map[string]string) *edge {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	m := New()
	m.base, m.check, m.poll, m.propagate = srv.URL, &http.Client{Transport: f}, time.Millisecond, 50*time.Millisecond
	p, err := m.edgeFactory(t.Context(), providers.Instance{Config: config, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	e := p.(*edge)
	e.client = srv.Client()
	return e
}

func problemKey(err error) i18n.Key {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p.Key
	}
	return ""
}

// A website on a custom hostname of Cloudflare for SaaS, behind a zone
// that challenges strangers: everything is set up the first time, through
// the route of the path on every host and a rule letting the files past.
func TestEdgeSaaS(t *testing.T) {
	f := &fakeEdge{zones: map[string]string{"saas.example.net": "z1"}, saas: true, kv: map[string][]byte{}, rules: []map[string]any{}}
	e := newEdge(t, f, map[string]string{"zone": "saas.example.net", "domain": "shop.example.org"})
	ctx := t.Context()

	file := contracts.File{Domain: "Shop.example.org", Path: "/.well-known/pki-validation/abc.txt", Content: []byte("hello")}
	if err := e.PutFile(ctx, file); err != nil {
		t.Fatal(err)
	}
	if string(f.kv["shop.example.org/.well-known/pki-validation/abc.txt"]) != "hello" || f.binding != "ns1" || f.scripts != 1 {
		t.Errorf("kv %v, binding %q, %d uploads", f.kv, f.binding, f.scripts)
	}
	if len(f.routes) != 1 || f.routes[0]["pattern"] != "*/.well-known/*" || f.routes[0]["script"] != scriptName {
		t.Errorf("routes %v", f.routes)
	}
	if len(f.rules) != 1 || f.rules[0]["expression"] != `(http.host in {"shop.example.org"} and starts_with(http.request.uri.path, "/.well-known/"))` ||
		f.rules[0]["action"] != "skip" {
		t.Errorf("rules %v", f.rules)
	}

	// Another domain: the rule takes it in, nothing else is made again.
	if err := e.PutFile(ctx, contracts.File{Domain: "b.example.org", Path: "/.well-known/x", Content: []byte("b")}); err != nil {
		t.Fatal(err)
	}
	if len(f.routes) != 1 || f.scripts != 1 || len(f.rules) != 1 ||
		f.rules[0]["expression"] != `(http.host in {"b.example.org" "shop.example.org"} and starts_with(http.request.uri.path, "/.well-known/"))` {
		t.Errorf("routes %v, %d uploads, rules %v", f.routes, f.scripts, f.rules)
	}

	if err := e.RemoveFile(ctx, file); err != nil || f.kv["shop.example.org/.well-known/pki-validation/abc.txt"] != nil {
		t.Errorf("remove: %v %v", err, f.kv)
	}
	if msg, err := e.Test(ctx); err != nil || msg.Key != i18n.CloudflareTestEdgeOk {
		t.Errorf("test: %v %v", msg, err)
	}
}

// A zone found by the domain, which takes a route of the host itself, on
// a plan whose rules may not skip phases, with custom rules of its own.
func TestEdgeZoneOfDomain(t *testing.T) {
	theirs := map[string]any{"id": "theirs", "expression": `(ip.src.country ne "CN")`, "action": "managed_challenge"}
	f := &fakeEdge{zones: map[string]string{"example.com": "z2"}, kv: map[string][]byte{}, rules: []map[string]any{theirs}, noPhase: true}
	e := newEdge(t, f, map[string]string{})
	if err := e.PutFile(t.Context(), contracts.File{Domain: "www.example.com", Path: "/.well-known/acme-challenge/t", Content: []byte("k")}); err != nil {
		t.Fatal(err)
	}
	if len(f.routes) != 1 || f.routes[0]["pattern"] != "www.example.com/.well-known/*" {
		t.Errorf("routes %v", f.routes)
	}
	// JEC's rule comes first, so that it skips theirs.
	params, _ := f.rules[0]["action_parameters"].(map[string]any)
	if len(f.rules) != 2 || f.rules[0]["ref"] != ruleRef || f.rules[1]["id"] != "theirs" || params["phases"] != nil || params["ruleset"] != "current" {
		t.Errorf("rules %v", f.rules)
	}
	if _, err := e.Test(t.Context()); problemKey(err) != i18n.CloudflareErrorNoDomain {
		t.Errorf("test without a domain: %v", err)
	}
}

func TestEdgeFailures(t *testing.T) {
	ctx := t.Context()
	file := contracts.File{Domain: "www.example.com", Path: "/.well-known/x", Content: []byte("x")}

	// No zone: say how to name it.
	e := newEdge(t, &fakeEdge{zones: map[string]string{}, kv: map[string][]byte{}}, map[string]string{})
	if err := e.PutFile(ctx, file); problemKey(err) != i18n.CloudflareErrorEdgeZone {
		t.Errorf("no zone: %v", err)
	}

	// Another Worker has the route: left alone.
	f := &fakeEdge{zones: map[string]string{"example.com": "z"}, kv: map[string][]byte{},
		routes: []map[string]string{{"pattern": "www.example.com/.well-known/*", "script": "theirs"}}}
	if err := newEdge(t, f, map[string]string{}).PutFile(ctx, file); problemKey(err) != i18n.CloudflareErrorRouteTaken {
		t.Errorf("route taken: %v", err)
	}

	// The token may not edit the zone's rules, and the file does not show:
	// the zone's security may be why.
	f = &fakeEdge{zones: map[string]string{"example.com": "z"}, kv: map[string][]byte{}, noWAF: true}
	e = newEdge(t, f, map[string]string{})
	f.binding = "elsewhere" // the edge does not serve the file
	e.scripts["acc"] = true
	if err := e.PutFile(ctx, file); problemKey(err) != i18n.CloudflareErrorNotServedClosed {
		t.Errorf("not served, rule refused: %v", err)
	}

	// A file outside /.well-known/.
	if err := e.PutFile(ctx, contracts.File{Domain: "www.example.com", Path: "/index.html"}); problemKey(err) != i18n.CloudflareErrorFilePath {
		t.Errorf("bad path: %v", err)
	}

	// A refused token ends the attempts.
	e = newEdge(t, &fakeEdge{zones: map[string]string{"example.com": "z"}, kv: map[string][]byte{}}, map[string]string{})
	e.token = "wrong"
	if err := e.PutFile(ctx, file); problemKey(err) != i18n.CloudflareErrorAuth {
		t.Errorf("refused token: %v", err)
	}
}

// The email of an account with its Global API Key signs in as a token
// does; with neither, the instance says what to enter.
func TestEdgeGlobalKey(t *testing.T) {
	f := &fakeEdge{zones: map[string]string{"example.com": "z"}, kv: map[string][]byte{}}
	e := newEdgeSigned(t, f, map[string]string{"api_email": "me@example.com", "domain": "www.example.com"}, map[string]string{"api_key": "k3y"})
	if msg, err := e.Test(t.Context()); err != nil || msg.Key != i18n.CloudflareTestEdgeOk {
		t.Fatalf("global key: %v %v", msg, err)
	}
	m := New()
	if _, err := m.edgeFactory(t.Context(), providers.Instance{Config: map[string]string{"api_email": "me@example.com"}}); problemKey(err) != i18n.CloudflareErrorSignIn {
		t.Errorf("no key: %v", err)
	}
	if _, err := factory(t.Context(), providers.Instance{Config: map[string]string{"zone": "example.com"}}); problemKey(err) != i18n.CloudflareErrorSignIn {
		t.Errorf("custom, nothing to sign in with: %v", err)
	}
}

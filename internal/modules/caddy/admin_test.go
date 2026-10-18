package caddy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeCaddy is the admin API of a Caddy, as far as JEC uses it: its config
// as JSON, which a change replaces only when Caddy would load it, the
// certificates loaded from PEM being whole pairs.
type fakeCaddy struct {
	mu     sync.Mutex
	config any
}

func (f *fakeCaddy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fail := func(status int, msg string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	if r.Header.Get("Origin") != "" {
		fail(http.StatusForbidden, "client is not allowed to access from origin")
		return
	}
	var body any
	if r.Body != nil {
		data, _ := io.ReadAll(r.Body)
		if len(data) > 0 && json.Unmarshal(data, &body) != nil {
			fail(http.StatusBadRequest, "bad JSON")
			return
		}
	}
	// Each change works on a copy, kept if Caddy would load it.
	b, _ := json.Marshal(f.config)
	var next any
	_ = json.Unmarshal(b, &next)
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/config/"):
		v, err := get(f.config, segments(r.URL.Path))
		if err != nil {
			fail(http.StatusBadRequest, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(v)
		return
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/config/"):
		var err error
		if next, err = post(next, segments(r.URL.Path), body); err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/id/"):
		id := strings.TrimPrefix(r.URL.Path, "/id/")
		if !replaceID(next, id, body) {
			fail(http.StatusNotFound, "unknown object ID '"+id+"'")
			return
		}
	default:
		fail(http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := loads(next); err != nil {
		fail(http.StatusInternalServerError, "loading new config: "+err.Error())
		return
	}
	f.config = next
}

func segments(path string) []string {
	return strings.Split(strings.Trim(strings.TrimPrefix(path, "/config/"), "/"), "/")
}

// get follows path down v: null for a key missing from an object, an
// error under a missing key.
func get(v any, path []string) (any, error) {
	for i, key := range path {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("invalid traversal path at: config/" + strings.Join(path[:i+1], "/"))
		}
		v = obj[key]
	}
	return v, nil
}

// post sets path in v to body, or appends body to the array there.
func post(v any, path []string, body any) (any, error) {
	if len(path) == 0 || path[0] == "" {
		return body, nil
	}
	parent, err := get(v, path[:len(path)-1])
	obj, ok := parent.(map[string]any)
	if err != nil || !ok {
		return nil, errors.New("invalid traversal path at: config/" + strings.Join(path, "/"))
	}
	key := path[len(path)-1]
	if arr, ok := obj[key].([]any); ok {
		obj[key] = append(arr, body)
	} else {
		obj[key] = body
	}
	return v, nil
}

// replaceID replaces the object of @id id anywhere in v with body.
func replaceID(v any, id string, body any) bool {
	switch v := v.(type) {
	case map[string]any:
		for k, c := range v {
			if o, ok := c.(map[string]any); ok && o["@id"] == id {
				v[k] = body
				return true
			}
			if replaceID(c, id, body) {
				return true
			}
		}
	case []any:
		for i, c := range v {
			if o, ok := c.(map[string]any); ok && o["@id"] == id {
				v[i] = body
				return true
			}
			if replaceID(c, id, body) {
				return true
			}
		}
	}
	return false
}

// loads checks what Caddy checks of the certificates loaded from PEM.
func loads(config any) error {
	v, _ := get(config, loadPEM)
	if v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return errors.New("load_pem is no array")
	}
	for i, e := range list {
		o, _ := e.(map[string]any)
		cert, _ := o["certificate"].(string)
		key, _ := o["key"].(string)
		if _, err := tls.X509KeyPair([]byte(cert), []byte(key)); err != nil {
			return errors.New("PEM pair " + strconv.Itoa(i) + ": " + err.Error())
		}
	}
	return nil
}

// loaded returns the certificates of load_pem, by @id.
func (f *fakeCaddy) loaded(t *testing.T) map[string]*x509.Certificate {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	v, _ := get(f.config, loadPEM)
	out := map[string]*x509.Certificate{}
	list, _ := v.([]any)
	for _, e := range list {
		o := e.(map[string]any)
		pair, err := tls.X509KeyPair([]byte(o["certificate"].(string)), []byte(o["key"].(string)))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := o["@id"].(string)
		out[id] = pair.Leaf
	}
	return out
}

func serve(t *testing.T, f *fakeCaddy) string {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv.URL
}

func openAdmin(t *testing.T, url string) *admin {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": url}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*admin)
}

func request(t *testing.T, id int64) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.test")
	return contracts.DeployRequest{CertificateID: id, Domains: []string{"example.test"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func problemKey(err error) i18n.Key {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p.Key
	}
	return ""
}

func TestDeploy(t *testing.T) {
	for name, config := range map[string]string{
		"no tls app":       `{"apps":{"http":{"servers":{}}}}`,
		"no certificates":  `{"apps":{"tls":{"automation":{}}}}`,
		"no load_pem":      `{"apps":{"tls":{"certificates":{"load_files":[]}}}}`,
		"empty load_pem":   `{"apps":{"tls":{"certificates":{"load_pem":[]}}}}`,
		"no apps":          `{"admin":{"listen":"0.0.0.0:2019"}}`,
		"somebody's entry": `{"apps":{"tls":{"certificates":{"load_pem":[{"@id":"theirs"}]}}}}`,
	} {
		f := &fakeCaddy{}
		if err := json.Unmarshal([]byte(config), &f.config); err != nil {
			t.Fatal(err)
		}
		theirs := 0
		if list, _ := get(f.config, loadPEM); list != nil {
			// The entry of somebody else, which stays as it is.
			for _, e := range list.([]any) {
				other := request(t, 9)
				e.(map[string]any)["certificate"], e.(map[string]any)["key"] = cryptoxtest.CertPEM(other.Chain[0]), cryptoxtest.KeyPEM(t, other.Key)
				theirs++
			}
		}
		a := openAdmin(t, serve(t, f))

		// The first version is added, the next replaces it in place.
		first := request(t, 4)
		var keys []i18n.Key
		first.Log = func(l contracts.LogLine) { keys = append(keys, l.Key) }
		res, err := a.Deploy(t.Context(), first)
		if err != nil || res.Ref != "jec-4" {
			t.Fatalf("%s: %+v %v", name, res, err)
		}
		if got := f.loaded(t); len(got) != 1+theirs || !got["jec-4"].Equal(first.Chain[0]) {
			t.Errorf("%s: loaded %v", name, got)
		}
		if strings.Join(stringsOf(keys), " ") != "caddy.log.replace caddy.log.add caddy.log.loaded" {
			t.Errorf("%s: log %v", name, keys)
		}
		next := request(t, 4)
		if _, err := a.Deploy(t.Context(), next); err != nil {
			t.Fatalf("%s: next: %v", name, err)
		}
		if got := f.loaded(t); len(got) != 1+theirs || !got["jec-4"].Equal(next.Chain[0]) {
			t.Errorf("%s: loaded %v", name, got)
		}
		// Another certificate goes beside it.
		if _, err := a.Deploy(t.Context(), request(t, 5)); err != nil || len(f.loaded(t)) != 2+theirs {
			t.Errorf("%s: another: %v", name, err)
		}
	}
}

func stringsOf(keys []i18n.Key) []string {
	var out []string
	for _, k := range keys {
		out = append(out, string(k))
	}
	return out
}

func TestDeployFailures(t *testing.T) {
	// A Caddy with no config at all is not given one.
	f := &fakeCaddy{}
	_, err := openAdmin(t, serve(t, f)).Deploy(t.Context(), request(t, 4))
	if problemKey(err) != i18n.CaddyErrorApi || !retry.IsPermanent(err) || f.config != nil {
		t.Errorf("no config: %v, config %v", err, f.config)
	}

	// Caddy refuses a pair it cannot load, and keeps its config.
	f = &fakeCaddy{config: map[string]any{"apps": map[string]any{}}}
	req := request(t, 4)
	req.Key = cryptoxtest.NewKey(t)
	_, err = openAdmin(t, serve(t, f)).Deploy(t.Context(), req)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.CaddyErrorApi || !retry.IsPermanent(err) || !strings.Contains(p.Params["error"].(string), "PEM pair") {
		t.Errorf("bad pair: %v", err)
	}
	if v, _ := get(f.config, []string{"apps", "tls"}); v != nil {
		t.Errorf("config changed: %v", f.config)
	}

	// Nothing answers.
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	_, err = openAdmin(t, srv.URL).Deploy(t.Context(), request(t, 4))
	if problemKey(err) != i18n.CaddyErrorConnect || retry.IsPermanent(err) {
		t.Errorf("closed: %v", err)
	}
	// Plain HTTP to the public internet is refused before it connects.
	_, err = openAdmin(t, "http://192.0.2.1:2019").Deploy(t.Context(), request(t, 4))
	if problemKey(err) != i18n.CaddyErrorPlain || !retry.IsPermanent(err) {
		t.Errorf("public: %v", err)
	}
}

func TestURL(t *testing.T) {
	for _, raw := range []string{"https://caddy:2019", "ftp://caddy", "http://caddy:2019/config", "http://user@caddy:2019", "unix://", "unix://host/sock", "caddy:2019", "http://caddy:2019?x=1"} {
		_, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": raw}})
		if problemKey(err) != i18n.CaddyErrorUrl || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{"http://caddy:2019", "http://10.0.0.2:2019/", "unix:///run/caddy/admin.sock"} {
		if _, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": raw}}); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.sock")
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", path)
	if err != nil {
		t.Skip("no unix sockets here:", err)
	}
	f := &fakeCaddy{config: map[string]any{"apps": map[string]any{}}}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	a := openAdmin(t, "unix://"+path)
	if msg, err := a.Test(t.Context()); err != nil || msg.Key != i18n.CaddyTestOk {
		t.Fatalf("test: %+v %v", msg, err)
	}
	if _, err := a.Deploy(t.Context(), request(t, 4)); err != nil || len(f.loaded(t)) != 1 {
		t.Errorf("deploy: %v", err)
	}
}

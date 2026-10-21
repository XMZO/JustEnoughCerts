package kong

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeAdmin answers as Kong's Admin API does, keeping what is put, and
// what reply says for the rest. A token, when set, is asked for.
type fakeAdmin struct {
	t     *testing.T
	token string
	mu    sync.Mutex
	puts  map[string]map[string]any
	reply func(r *http.Request) (int, string)
}

func (f *fakeAdmin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Kong-Admin-Token") != f.token {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Invalid credentials. Token or User credentials required"}`)
		return
	}
	if f.reply != nil {
		if status, answer := f.reply(r); status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, answer)
			return
		}
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/":
		_, _ = io.WriteString(w, `{"version":"3.9.4","configuration":{}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/certificates":
		_, _ = io.WriteString(w, `{"data":[],"next":null}`)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/certificates/"):
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Error(err)
		}
		f.mu.Lock()
		f.puts[strings.TrimPrefix(r.URL.Path, "/certificates/")] = body
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"id":"x"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not found"}`)
	}
}

func serve(t *testing.T, f *fakeAdmin, config map[string]string) *gateway {
	t.Helper()
	f.t, f.puts = t, map[string]map[string]any{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	config["url"] = srv.URL
	g, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"admin_token": f.token}})
	if err != nil {
		t.Fatal(err)
	}
	return g.(*gateway)
}

func request(t *testing.T, domains ...string) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), domains...)
	return contracts.DeployRequest{CertificateID: 12, Domains: domains, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func TestNameUUID(t *testing.T) {
	// As Python's uuid.uuid5 makes it, in the same namespace.
	if got := nameUUID("jec-certificate-12"); got != "55289e4d-fa06-5a61-81a1-641198d772e6" {
		t.Errorf("UUID %s", got)
	}
}

func TestDeploy(t *testing.T) {
	f := &fakeAdmin{token: "s3cret"}
	g := serve(t, f, map[string]string{})
	req := request(t, "example.com", "*.example.com")
	if _, err := g.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	put := f.puts["55289e4d-fa06-5a61-81a1-641198d772e6"]
	key, _ := cryptox.EncodeKey(req.Key)
	snis, _ := put["snis"].([]any)
	tags, _ := put["tags"].([]any)
	if put == nil || put["cert"] != string(cryptox.EncodeCertificates(req.Chain...)) || put["key"] != string(key) ||
		!slices.Equal(snis, []any{"example.com", "*.example.com"}) || !slices.Equal(tags, []any{"jec"}) {
		t.Errorf("put %v", f.puts)
	}
	if msg, err := g.Test(t.Context()); err != nil || msg.Key != i18n.KongTestOk || msg.Params["version"] != "3.9.4" {
		t.Errorf("test %v %v", msg, err)
	}

	// An ID of the user's choosing, in any case.
	f2 := &fakeAdmin{}
	id := "0B5A1B4E-2C3D-4E5F-8A9B-0C1D2E3F4A5B"
	if _, err := serve(t, f2, map[string]string{"id": id}).Deploy(t.Context(), request(t, "shop.example")); err != nil || f2.puts[strings.ToLower(id)] == nil {
		t.Errorf("put %v: %v", f2.puts, err)
	}
}

func TestErrors(t *testing.T) {
	var p *contracts.Problem
	g := serve(t, &fakeAdmin{token: "s3cret"}, map[string]string{})
	g.token = "wrong"
	if _, err := g.Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.KongErrorToken || !retry.IsPermanent(err) {
		t.Errorf("a wrong token: %v", err)
	}
	for name, tt := range map[string]struct {
		status    int
		answer    string
		key       i18n.Key
		permanent bool
	}{
		"a name held": {http.StatusBadRequest, `{"code":2,"name":"schema violation","message":"schema violation (snis: www.example.com already associated with existing certificate 'x')"}`, i18n.KongErrorConflict, true},
		"DB-less":     {http.StatusMethodNotAllowed, `{"code":12,"name":"operation unsupported","message":"cannot create or update 'certificates' entities when not using a database"}`, i18n.KongErrorDbless, true},
		"refused":     {http.StatusBadRequest, `{"code":2,"message":"2 schema violations"}`, i18n.KongErrorApi, true},
		"down":        {http.StatusServiceUnavailable, `{"message":"An unexpected error occurred"}`, i18n.KongErrorApi, false},
		"not the API": {http.StatusNotFound, "<html>not found</html>", i18n.KongErrorStatus, true},
	} {
		g := serve(t, &fakeAdmin{reply: func(*http.Request) (int, string) { return tt.status, tt.answer }}, map[string]string{})
		_, err := g.Deploy(t.Context(), request(t, "example.com"))
		if !errors.As(err, &p) || p.Key != tt.key || retry.IsPermanent(err) != tt.permanent {
			t.Errorf("%s: %v (permanent %v)", name, err, retry.IsPermanent(err))
		}
	}
}

func TestFactory(t *testing.T) {
	var p *contracts.Problem
	for _, tt := range []struct {
		config map[string]string
		key    i18n.Key
	}{
		{map[string]string{"url": "ftp://gw.example"}, i18n.KongErrorUrl},
		{map[string]string{"url": "https://gw.example", "id": "jec-1"}, i18n.KongErrorId},
		{map[string]string{"url": "https://gw.example", "fingerprint": "nope"}, i18n.KongErrorFingerprint},
	} {
		if _, err := factory(t.Context(), providers.Instance{Config: tt.config}); !errors.As(err, &p) || p.Key != tt.key {
			t.Errorf("%v: %v", tt.config, err)
		}
	}
	for url, key := range map[string]i18n.Key{"http://203.0.113.5:8001": i18n.KongErrorPlain, "https://127.0.0.1:1": i18n.KongErrorPrivate} {
		g, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": url}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.(*gateway).Test(t.Context()); !errors.As(err, &p) || p.Key != key {
			t.Errorf("%s: %v", url, err)
		}
	}
}

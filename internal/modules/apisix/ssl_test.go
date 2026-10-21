package apisix

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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

const adminKey = "s3cret-admin-key"

// fakeAdmin answers as APISIX's Admin API does, keeping what is put, and
// what reply says for the rest.
type fakeAdmin struct {
	t     *testing.T
	mu    sync.Mutex
	puts  map[string]map[string]any
	reply func(r *http.Request) (int, string)
}

func (f *fakeAdmin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-API-KEY") != adminKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error_msg":"failed to check token"}`)
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
	case r.Method == http.MethodGet && r.URL.Path == "/apisix/admin/ssls":
		_, _ = io.WriteString(w, `{"list":[{"key":"/apisix/ssls/a"},{"key":"/apisix/ssls/b"}],"total":2}`)
	case r.Method == http.MethodPut && len(r.URL.Path) > len("/apisix/admin/ssls/"):
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Error(err)
		}
		f.mu.Lock()
		f.puts[r.URL.Path[len("/apisix/admin/ssls/"):]] = body
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"key":"/apisix/ssls/x","value":{}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error_msg":"404 Route Not Found"}`)
	}
}

func serve(t *testing.T, f *fakeAdmin, config map[string]string) *gateway {
	t.Helper()
	f.t, f.puts = t, map[string]map[string]any{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	config["url"] = srv.URL
	g, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"admin_key": adminKey}})
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

func TestDeploy(t *testing.T) {
	f := &fakeAdmin{}
	g := serve(t, f, map[string]string{})
	req := request(t, "example.com", "*.example.com")
	var lines []contracts.LogLine
	req.Log = func(l contracts.LogLine) { lines = append(lines, l) }
	if _, err := g.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	put := f.puts["jec-12"]
	key, _ := cryptox.EncodeKey(req.Key)
	snis, _ := put["snis"].([]any)
	if put == nil || put["cert"] != string(cryptox.EncodeCertificates(req.Chain...)) || put["key"] != string(key) ||
		!slices.Equal(snis, []any{"example.com", "*.example.com"}) {
		t.Errorf("put %v", f.puts)
	}
	if len(lines) != 1 || lines[0].Key != i18n.ApisixLogPut || lines[0].Params["id"] != "jec-12" {
		t.Errorf("log %v", lines)
	}

	// An ID of the user's choosing.
	f2 := &fakeAdmin{}
	if _, err := serve(t, f2, map[string]string{"id": "shop_cert-1"}).Deploy(t.Context(), request(t, "shop.example")); err != nil || f2.puts["shop_cert-1"] == nil {
		t.Errorf("put %v: %v", f2.puts, err)
	}
}

func TestTest(t *testing.T) {
	if msg, err := serve(t, &fakeAdmin{}, map[string]string{}).Test(t.Context()); err != nil || msg.Key != i18n.ApisixTestOk || msg.Params["count"] != 2 {
		t.Errorf("test %v %v", msg, err)
	}
}

func TestErrors(t *testing.T) {
	var p *contracts.Problem
	g := serve(t, &fakeAdmin{}, map[string]string{})
	g.key = "wrong"
	if _, err := g.Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.ApisixErrorKey || !retry.IsPermanent(err) {
		t.Errorf("a wrong key: %v", err)
	}
	for name, tt := range map[string]struct {
		status    int
		answer    string
		key       i18n.Key
		permanent bool
	}{
		"refused":       {http.StatusBadRequest, `{"error_msg":"invalid configuration: failed to parse cert"}`, i18n.ApisixErrorApi, true},
		"etcd down":     {http.StatusServiceUnavailable, `{"error_msg":"failed to connect etcd"}`, i18n.ApisixErrorApi, false},
		"not the API":   {http.StatusNotFound, "<html>not found</html>", i18n.ApisixErrorStatus, true},
		"a bad gateway": {http.StatusBadGateway, "<html>502</html>", i18n.ApisixErrorStatus, false},
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
		{map[string]string{"url": "ftp://gw.example"}, i18n.ApisixErrorUrl},
		{map[string]string{"url": "https://gw.example", "id": "has space"}, i18n.ApisixErrorId},
		{map[string]string{"url": "https://gw.example", "fingerprint": "nope"}, i18n.ApisixErrorFingerprint},
	} {
		if _, err := factory(t.Context(), providers.Instance{Config: tt.config}); !errors.As(err, &p) || p.Key != tt.key {
			t.Errorf("%v: %v", tt.config, err)
		}
	}
	for url, key := range map[string]i18n.Key{"http://203.0.113.5:9180": i18n.ApisixErrorPlain, "https://127.0.0.1:1": i18n.ApisixErrorPrivate} {
		g, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": url}, Secrets: map[string]string{"admin_key": adminKey}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.(*gateway).Test(t.Context()); !errors.As(err, &p) || p.Key != key {
			t.Errorf("%s: %v", url, err)
		}
	}
}

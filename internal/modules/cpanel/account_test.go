package cpanel

import (
	"crypto/x509"
	"errors"
	"io"
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
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

const token = "TOKEN123"

// fakeCpanel answers as UAPI does, with what reply says.
type fakeCpanel struct {
	t     *testing.T
	mu    sync.Mutex
	calls []string
	got   []url.Values
	reply func(fn string, form url.Values) (int, string)
}

func (f *fakeCpanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "cpanel jec:"+token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		f.t.Error(err)
	}
	fn := strings.TrimPrefix(r.URL.Path, "/execute/")
	f.mu.Lock()
	f.calls = append(f.calls, fn)
	f.got = append(f.got, r.PostForm)
	f.mu.Unlock()
	status, body := f.reply(fn, r.PostForm)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func serve(t *testing.T, reply func(string, url.Values) (int, string)) (*fakeCpanel, map[string]string) {
	t.Helper()
	f := &fakeCpanel{t: t, reply: reply}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, map[string]string{"url": srv.URL + "/", "username": "jec"}
}

func open(t *testing.T, config map[string]string, secret string) *account {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"api_token": secret}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*account)
}

func request(t *testing.T, domains ...string) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), domains...)
	return contracts.DeployRequest{CertificateID: 1, Domains: domains, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func problemKey(err error) i18n.Key {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p.Key
	}
	return ""
}

func TestDeploy(t *testing.T) {
	f, config := serve(t, func(fn string, _ url.Values) (int, string) {
		if fn == "SSL/installed_hosts" {
			return 200, `{"status":1,"errors":null,"data":[{"servername":"example.com"},{"servername":"blog.example.com"}]}`
		}
		return 200, `{"status":1,"errors":null,"data":{"status":1,"message":"installed"}}`
	})
	a := open(t, config, token)
	msg, err := a.Test(t.Context())
	if err != nil || msg.Key != i18n.CpanelTestOk || msg.Params["count"] != 2 || msg.Params["user"] != "jec" {
		t.Fatalf("test: %+v, %v", msg, err)
	}

	// Without websites set, the one of the primary domain, a wildcard's
	// without its star; the leaf apart from its intermediates.
	req := request(t, "*.example.com", "example.com")
	var lines []i18n.Key
	req.Log = func(l contracts.LogLine) { lines = append(lines, l.Key) }
	if _, err := a.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	got := f.got[len(f.got)-1]
	if f.calls[len(f.calls)-1] != "SSL/install_ssl" || got.Get("domain") != "example.com" ||
		got.Get("cert") != string(cryptox.EncodeCertificates(req.Chain[0])) || got.Get("cabundle") != string(cryptox.EncodeCertificates(req.Chain[1])) ||
		!strings.Contains(got.Get("key"), "PRIVATE KEY") || !slices.Equal(lines, []i18n.Key{i18n.CpanelLogInstall}) {
		t.Errorf("installed %v, log %v", got, lines)
	}

	// The websites set, each.
	config["domains"] = "Shop.example.com, blog.example.com"
	f.got = nil
	if _, err := open(t, config, token).Deploy(t.Context(), request(t, "shop.example.com", "blog.example.com")); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 2 || f.got[0].Get("domain") != "shop.example.com" || f.got[1].Get("domain") != "blog.example.com" {
		t.Errorf("installed on %v", f.got)
	}
}

func TestFailures(t *testing.T) {
	_, config := serve(t, func(string, url.Values) (int, string) {
		return 200, `{"status":0,"errors":["The certificate does not match the domain."],"data":null}`
	})
	for name, c := range map[string]struct {
		config map[string]string
		secret string
		key    i18n.Key
		retry  bool
	}{
		"refused":     {config, token, i18n.CpanelErrorApi, false},
		"wrong token": {config, "nope", i18n.CpanelErrorToken, false},
	} {
		_, err := open(t, c.config, c.secret).Deploy(t.Context(), request(t, "example.com"))
		if problemKey(err) != c.key || retry.IsPermanent(err) == c.retry {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, broken := serve(t, func(string, url.Values) (int, string) { return 500, "oops" })
	if _, err := open(t, broken, token).Test(t.Context()); problemKey(err) != i18n.CpanelErrorStatus || retry.IsPermanent(err) {
		t.Errorf("a server error: %v", err)
	}
	if _, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": "ftp://example.com"}}); problemKey(err) != i18n.CpanelErrorUrl {
		t.Errorf("not an address: %v", err)
	}
	// Plain HTTP to the public internet would carry the key in the clear.
	if _, err := open(t, map[string]string{"url": "http://203.0.113.5:2082", "username": "jec"}, token).Test(t.Context()); problemKey(err) != i18n.CpanelErrorPlain {
		t.Errorf("plain HTTP to a public address: %v", err)
	}
}

// TestSelfSigned reaches a server with a certificate of its own making:
// unverified, its fingerprint is told, and pinned, it is trusted.
func TestSelfSigned(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":1,"errors":null,"data":[]}`)
	}))
	t.Cleanup(srv.Close)
	config := map[string]string{"url": srv.URL, "username": "jec", "allow_private": "true"}
	_, err := open(t, config, token).Test(t.Context())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.CpanelErrorUnverified {
		t.Fatalf("unverified: %v", err)
	}
	config["fingerprint"], _ = p.Params["fingerprint"].(string)
	if msg, err := open(t, config, token).Test(t.Context()); err != nil || msg.Params["count"] != 0 {
		t.Errorf("pinned: %+v, %v", msg, err)
	}
}

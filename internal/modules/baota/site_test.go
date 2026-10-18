package baota

import (
	"crypto/md5"
	"crypto/x509"
	"encoding/hex"
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
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

const apiKey = "s3cret-api-key"

var now = time.Unix(1760000000, 0)

func sum(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// fakePanel answers as a BaoTa panel does, with what reply says.
type fakePanel struct {
	t     *testing.T
	mu    sync.Mutex
	got   []url.Values
	reply func(action string, form url.Values) (int, string)
}

func (f *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.t.Error(err)
	}
	form := r.PostForm
	if form.Get("request_time") != "1760000000" || form.Get("request_token") != sum("1760000000"+sum(apiKey)) {
		f.t.Errorf("signed %v", form)
	}
	f.mu.Lock()
	f.got = append(f.got, form)
	f.mu.Unlock()
	status, body := f.reply(r.URL.Path+"?"+r.URL.RawQuery, form)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func open(t *testing.T, config map[string]string) *panel {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"api_key": apiKey}})
	if err != nil {
		t.Fatal(err)
	}
	pl := p.(*panel)
	pl.now = func() time.Time { return now }
	return pl
}

func serve(t *testing.T, reply func(string, url.Values) (int, string)) (*fakePanel, map[string]string) {
	t.Helper()
	f := &fakePanel{t: t, reply: reply}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, map[string]string{"url": srv.URL + "/", "sites": "example.com, www.example.com"}
}

func request(t *testing.T) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com")
	return contracts.DeployRequest{CertificateID: 1, Domains: []string{"example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func TestDeploy(t *testing.T) {
	old := pause
	pause = time.Millisecond
	t.Cleanup(func() { pause = old })
	f, config := serve(t, func(action string, _ url.Values) (int, string) {
		if action != "/site?action=SetSSL" {
			return http.StatusNotFound, "no"
		}
		return http.StatusOK, `{"status": true, "msg": "ok"}`
	})
	req := request(t)
	var sites []string
	req.Log = func(l contracts.LogLine) { sites = append(sites, l.Params["site"].(string)) }
	if _, err := open(t, config).Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 2 || !slices.Equal(sites, []string{"example.com", "www.example.com"}) {
		t.Fatalf("sent %d, log %v", len(f.got), sites)
	}
	form := f.got[1]
	b, err := cryptox.ParseBundle([]byte(form.Get("csr")), []byte(form.Get("key")))
	if err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 || form.Get("type") != "0" || form.Get("siteName") != "www.example.com" {
		t.Errorf("sent %v: %v", form, err)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{200, `{"status": false, "msg": "IP check failed, your IP is [127.0.0.1]"}`, i18n.BaotaErrorIp, true},
		{200, `{"status": false, "msg": "the site does not exist"}`, i18n.BaotaErrorApi, true},
		{404, `<html>not found</html>`, i18n.BaotaErrorStatus, true},
		{502, `<html>bad gateway</html>`, i18n.BaotaErrorStatus, false},
	} {
		_, config := serve(t, func(string, url.Values) (int, string) { return c.status, c.body })
		_, err := open(t, config).Deploy(t.Context(), request(t))
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
}

func TestTest(t *testing.T) {
	f, config := serve(t, func(action string, _ url.Values) (int, string) {
		if action != "/system?action=GetSystemTotal" {
			return http.StatusNotFound, "no"
		}
		return http.StatusOK, `{"system": "Ubuntu 24.04", "version": "9.6.0", "mem_total": 1024}`
	})
	msg, err := open(t, config).Test(t.Context())
	if err != nil || msg.Key != i18n.BaotaTestOk || len(f.got) != 1 {
		t.Errorf("test %+v %v", msg, err)
	}
}

// A panel with a certificate of its own making: the test gives its
// fingerprint, which, once entered, JEC trusts, and only it.
func TestOwnCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(&fakePanel{t: t, reply: func(string, url.Values) (int, string) { return http.StatusOK, `{"version": "9.6.0"}` }})
	defer srv.Close()
	config := map[string]string{"url": srv.URL, "sites": "example.com", "allow_private": "true"}
	_, err := open(t, config).Test(t.Context())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.BaotaErrorUnverified || !retry.IsPermanent(err) {
		t.Fatalf("unverified: %v", err)
	}
	config["fingerprint"] = p.Params["fingerprint"].(string)
	if _, err := open(t, config).Test(t.Context()); err != nil {
		t.Errorf("pinned: %v", err)
	}
	config["fingerprint"] = strings.Repeat("00", 32)
	if _, err := open(t, config).Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.BaotaErrorPin {
		t.Errorf("another certificate: %v", err)
	}
}

func TestAddresses(t *testing.T) {
	for _, c := range []struct {
		url string
		key i18n.Key
	}{
		{"http://93.184.215.14:8888", i18n.BaotaErrorPlain},
		{"https://127.0.0.1:1", i18n.BaotaErrorPrivate},
	} {
		_, err := open(t, map[string]string{"url": c.url, "sites": "example.com"}).Test(t.Context())
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", c.url, err)
		}
	}
}

func TestSettings(t *testing.T) {
	for _, config := range []map[string]string{
		{"url": "203.0.113.5:8888", "sites": "a"},
		{"url": "ftp://203.0.113.5", "sites": "a"},
		{"url": "https://203.0.113.5:8888", "sites": "a", "fingerprint": "not one"},
	} {
		if _, err := factory(t.Context(), providers.Instance{Config: config}); err == nil || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", config, err)
		}
	}
	_, err := open(t, map[string]string{"url": "https://203.0.113.5:8888", "sites": " , "}).Deploy(t.Context(), request(t))
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.BaotaErrorSites {
		t.Errorf("no sites: %v", err)
	}
	if _, ok := httpx.Fingerprint(errors.New("other")); ok {
		t.Error("a fingerprint of nothing")
	}
}

package safeline

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

const token = "s3cret-token"

// fakeFirewall answers as SafeLine does, with its certificates, and what
// reply says for the rest.
type fakeFirewall struct {
	t     *testing.T
	mu    sync.Mutex
	certs string // the nodes listed
	posts []map[string]any
	reply func(r *http.Request) (int, string)
}

func (f *fakeFirewall) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-SLCE-API-TOKEN") != token {
		_, _ = io.WriteString(w, `{"data": null, "err": "login-required", "msg": "Login required"}`)
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
	case r.Method == http.MethodGet && r.URL.Path == "/api/open/cert":
		_, _ = io.WriteString(w, `{"data": {"nodes": [`+f.certs+`], "total": 0}, "err": null, "msg": ""}`)
	case r.Method == http.MethodPost && r.URL.Path == "/api/open/cert":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Error(err)
		}
		f.mu.Lock()
		f.posts = append(f.posts, body)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"data": 9, "err": null, "msg": ""}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "404 page not found")
	}
}

func serve(t *testing.T, f *fakeFirewall, config map[string]string) *firewall {
	t.Helper()
	f.t = t
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	config["url"] = srv.URL + "/"
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"api_token": token}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*firewall)
}

func request(t *testing.T, domains ...string) (contracts.DeployRequest, *[]contracts.LogLine) {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), domains...)
	var lines []contracts.LogLine
	return contracts.DeployRequest{CertificateID: 1, Domains: domains, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
		Log: func(l contracts.LogLine) { lines = append(lines, l) }}, &lines
}

func keys(lines []contracts.LogLine) []i18n.Key {
	var out []i18n.Key
	for _, l := range lines {
		out = append(out, l.Key)
	}
	return out
}

const listed3 = `{"id": 3, "type": 2, "domains": ["www.example.com", "example.com"], "related_sites": null},
	{"id": 4, "type": 3, "domains": ["EXAMPLE.com", "www.example.com"], "related_sites": ["shop"]},
	{"id": 5, "type": 2, "domains": ["other.example"], "related_sites": ["blog"]}`

func TestDeployReplacesByDomains(t *testing.T) {
	f := &fakeFirewall{certs: listed3}
	fw := serve(t, f, map[string]string{"allow_private": "true"})
	req, lines := request(t, "example.com", "www.example.com")
	if _, err := fw.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	// The one for the same domains that a site uses, replaced in place.
	key, _ := cryptox.EncodeKey(req.Key)
	if len(f.posts) != 1 {
		t.Fatalf("posts %v", f.posts)
	}
	got := f.posts[0]
	content, _ := got["manual"].(map[string]any)
	if got["id"] != float64(4) || got["type"] != float64(manual) || content["crt"] != string(cryptox.EncodeCertificates(req.Chain...)) || content["key"] != string(key) {
		t.Errorf("posted %v", got)
	}
	if k := keys(*lines); len(k) != 1 || k[0] != i18n.SafelineLogReplace {
		t.Errorf("log %v", k)
	}
}

func TestDeployAdds(t *testing.T) {
	f := &fakeFirewall{certs: listed3}
	fw := serve(t, f, map[string]string{"allow_private": "true"})
	req, lines := request(t, "new.example.com")
	if _, err := fw.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, has := f.posts[0]["id"]; has || len(f.posts) != 1 {
		t.Errorf("posted %v", f.posts)
	}
	if k := keys(*lines); len(k) != 2 || k[0] != i18n.SafelineLogAdd || k[1] != i18n.SafelineLogAdded || (*lines)[1].Params["id"] != "9" {
		t.Errorf("log %v %v", k, *lines)
	}
}

func TestDeployByID(t *testing.T) {
	f := &fakeFirewall{certs: listed3}
	fw := serve(t, f, map[string]string{"allow_private": "true", "certificate": "3"})
	req, lines := request(t, "anything.example")
	if _, err := fw.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if f.posts[0]["id"] != float64(3) {
		t.Errorf("posted %v", f.posts)
	}
	// Used by no site, it is told of.
	if k := keys(*lines); len(k) != 2 || k[1] != i18n.SafelineLogUnused {
		t.Errorf("log %v", k)
	}
	if msg, err := fw.Test(t.Context()); err != nil || msg.Key != i18n.SafelineTestOk {
		t.Errorf("test %v %v", msg, err)
	}

	gone := serve(t, &fakeFirewall{certs: listed3}, map[string]string{"allow_private": "true", "certificate": "7"})
	var p *contracts.Problem
	if _, err := gone.Deploy(t.Context(), req); !errors.As(err, &p) || p.Key != i18n.SafelineErrorNoCertificate || !retry.IsPermanent(err) {
		t.Errorf("a certificate not there: %v", err)
	}
	if _, err := gone.Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.SafelineErrorNoCertificate {
		t.Errorf("test of a certificate not there: %v", err)
	}
}

func TestTest(t *testing.T) {
	fw := serve(t, &fakeFirewall{certs: listed3}, map[string]string{"allow_private": "true"})
	if msg, err := fw.Test(t.Context()); err != nil || msg.Key != i18n.SafelineTestList || msg.Params["certificates"] != "#3 www.example.com, example.com; #4 EXAMPLE.com, www.example.com; #5 other.example" {
		t.Errorf("test %v %v", msg, err)
	}
	empty := serve(t, &fakeFirewall{}, map[string]string{"allow_private": "true"})
	if msg, err := empty.Test(t.Context()); err != nil || msg.Key != i18n.SafelineTestNone {
		t.Errorf("test of none %v %v", msg, err)
	}
}

func TestErrors(t *testing.T) {
	var p *contracts.Problem
	for name, tt := range map[string]struct {
		reply     func(r *http.Request) (int, string)
		key       i18n.Key
		permanent bool
	}{
		"refused": {
			func(*http.Request) (int, string) {
				return http.StatusOK, `{"data": null, "err": "login-required", "msg": "Login required"}`
			},
			i18n.SafelineErrorToken, true,
		},
		"a bad certificate": {
			func(r *http.Request) (int, string) {
				if r.Method == http.MethodPost {
					return http.StatusInternalServerError, `{"data": null, "err": "internal-error", "msg": "tls: private key does not match public key"}`
				}
				return 0, ""
			},
			i18n.SafelineErrorApi, true,
		},
		"down for a while": {
			func(*http.Request) (int, string) { return http.StatusBadGateway, "<html>502 Bad Gateway</html>" },
			i18n.SafelineErrorStatus, false,
		},
		"no API there": {
			func(*http.Request) (int, string) { return http.StatusNotFound, "404 page not found" },
			i18n.SafelineErrorStatus, true,
		},
	} {
		fw := serve(t, &fakeFirewall{certs: listed3, reply: tt.reply}, map[string]string{"allow_private": "true"})
		req, _ := request(t, "example.com", "www.example.com")
		_, err := fw.Deploy(t.Context(), req)
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
		{map[string]string{"url": "ftp://waf.example"}, i18n.SafelineErrorUrl},
		{map[string]string{"url": "https://waf.example", "certificate": "x"}, i18n.SafelineErrorCertificateId},
		{map[string]string{"url": "https://waf.example", "fingerprint": "nope"}, i18n.SafelineErrorFingerprint},
	} {
		if _, err := factory(t.Context(), providers.Instance{Config: tt.config}); !errors.As(err, &p) || p.Key != tt.key {
			t.Errorf("%v: %v", tt.config, err)
		}
	}
	// Plain HTTP reaches private addresses only.
	fw, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": "http://203.0.113.5:9443"}, Secrets: map[string]string{"api_token": token}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.(*firewall).Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.SafelineErrorPlain {
		t.Errorf("plain HTTP to a public address: %v", err)
	}
	// HTTPS reaches private addresses only when allowed.
	fw, err = factory(t.Context(), providers.Instance{Config: map[string]string{"url": "https://127.0.0.1:1"}, Secrets: map[string]string{"api_token": token}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.(*firewall).Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.SafelineErrorPrivate {
		t.Errorf("HTTPS to a private address: %v", err)
	}
}

func TestSameNames(t *testing.T) {
	for _, tt := range []struct {
		a, b []string
		want bool
	}{
		{[]string{"a.example", "B.example."}, []string{"b.example", "a.example"}, true},
		{[]string{"a.example"}, []string{"a.example", "b.example"}, false},
		{nil, nil, false},
	} {
		if got := sameNames(tt.a, tt.b); got != tt.want {
			t.Errorf("%v %v: %v", tt.a, tt.b, got)
		}
	}
}

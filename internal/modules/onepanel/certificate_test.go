package onepanel

import (
	"crypto/md5"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

const apiKey = "s3cret-api-key"

var now = time.Unix(1760000000, 0)

// fakePanel answers as a 1Panel does: two certificates, and what reply
// says for the rest.
type fakePanel struct {
	t       *testing.T
	version string
	mu      sync.Mutex
	paths   []string
	uploads []map[string]any
	reply   func(path string) (int, string)
}

func (f *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sum := md5.Sum([]byte("1panel" + apiKey + "1760000000"))
	if r.Header.Get("1Panel-Timestamp") != "1760000000" || r.Header.Get("1Panel-Token") != hex.EncodeToString(sum[:]) {
		f.t.Errorf("signed %v", r.Header)
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Error(err)
	}
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.mu.Unlock()
	if f.reply != nil {
		if status, answer := f.reply(r.URL.Path); status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, answer)
			return
		}
	}
	switch r.URL.Path {
	case "/api/" + f.version + "/websites/ssl/search":
		_, _ = io.WriteString(w, `{"code": 200, "message": "", "data": {"total": 2, "items": [
			{"id": 3, "primaryDomain": "example.com", "domains": "example.com,www.example.com", "description": "mine"},
			{"id": 5, "primaryDomain": "", "domains": "b.example", "description": ""}]}}`)
	case "/api/" + f.version + "/websites/ssl/upload":
		f.mu.Lock()
		f.uploads = append(f.uploads, body)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"code": 200, "message": "", "data": null}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "404 page not found")
	}
}

func serve(t *testing.T, f *fakePanel, config map[string]string) *panel {
	t.Helper()
	f.t = t
	if f.version == "" {
		f.version = apiV2
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := map[string]string{"url": srv.URL + "/", "api_version": f.version, "certificate": "3"}
	for k, v := range config {
		cfg[k] = v
	}
	p, err := factory(t.Context(), providers.Instance{Config: cfg, Secrets: map[string]string{"api_key": apiKey}})
	if err != nil {
		t.Fatal(err)
	}
	pl := p.(*panel)
	pl.now = func() time.Time { return now }
	return pl
}

func request(t *testing.T) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com")
	return contracts.DeployRequest{CertificateID: 1, Domains: []string{"example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func TestDeploy(t *testing.T) {
	for _, version := range []string{apiV2, apiV1} {
		f := &fakePanel{version: version}
		req := request(t)
		if _, err := serve(t, f, nil).Deploy(t.Context(), req); err != nil {
			t.Fatalf("%s: %v", version, err)
		}
		if len(f.uploads) != 1 {
			t.Fatalf("%s: uploads %v", version, f.uploads)
		}
		up := f.uploads[0]
		b, err := cryptox.ParseBundle([]byte(up["certificate"].(string)), []byte(up["privateKey"].(string)))
		if err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 || up["sslID"] != 3.0 || up["type"] != "paste" || up["description"] != "mine" {
			t.Errorf("%s: uploaded %v: %v", version, up, err)
		}
	}
}

func TestTest(t *testing.T) {
	msg, err := serve(t, &fakePanel{}, nil).Test(t.Context())
	if err != nil || msg.Key != i18n.OnepanelTestOk || msg.Params["certificate"] != "#3 example.com" {
		t.Errorf("found: %+v %v", msg, err)
	}
	msg, err = serve(t, &fakePanel{}, map[string]string{"certificate": ""}).Test(t.Context())
	if err != nil || msg.Key != i18n.OnepanelTestList || msg.Params["certificates"] != "#3 example.com, #5 b.example" {
		t.Errorf("listed: %+v %v", msg, err)
	}
	_, err = serve(t, &fakePanel{}, map[string]string{"certificate": "9"}).Test(t.Context())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.OnepanelErrorNoCertificate || !strings.Contains(p.Params["certificates"].(string), "#5") {
		t.Errorf("missing: %v", err)
	}
	_, err = serve(t, &fakePanel{}, map[string]string{"certificate": ""}).Deploy(t.Context(), request(t))
	if !errors.As(err, &p) || p.Key != i18n.OnepanelErrorCertificateMissing {
		t.Errorf("no ID: %v", err)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		version   string
		key       i18n.Key
		permanent bool
	}{
		{200, `{"code": 401, "message": "API key mismatch"}`, apiV2, i18n.OnepanelErrorKey, true},
		{401, `{"code": 401, "message": "unauthorized"}`, apiV2, i18n.OnepanelErrorKey, true},
		{200, `{"code": 500, "message": "database is locked"}`, apiV2, i18n.OnepanelErrorApi, false},
		{200, `{"code": 400, "message": "bad certificate"}`, apiV2, i18n.OnepanelErrorApi, true},
		{502, `<html>bad gateway</html>`, apiV2, i18n.OnepanelErrorStatus, false},
		// A panel of the other version has no such path.
		{0, ``, apiV1, i18n.OnepanelErrorVersion, true},
	} {
		f := &fakePanel{version: apiV2, reply: func(string) (int, string) { return c.status, c.body }}
		p := serve(t, f, map[string]string{"api_version": c.version})
		_, err := p.Deploy(t.Context(), request(t))
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s %s: %v", c.status, c.body, c.version, err)
		}
	}
}

func TestSettings(t *testing.T) {
	for _, config := range []map[string]string{
		{"url": "203.0.113.5:10086"},
		{"url": "https://203.0.113.5:10086", "fingerprint": "not one"},
		{"url": "https://203.0.113.5:10086", "certificate": "three"},
		{"url": "https://203.0.113.5:10086", "certificate": "-1"},
	} {
		if _, err := factory(t.Context(), providers.Instance{Config: config}); err == nil || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", config, err)
		}
	}
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": "http://93.184.215.14:10086", "certificate": "3"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.(*panel).Test(t.Context())
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.OnepanelErrorPlain {
		t.Errorf("plain HTTP to a public address: %v", err)
	}
}

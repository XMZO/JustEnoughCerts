package baiducloud

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	bceauth "github.com/baidubce/bce-sdk-go/auth"
	bcehttp "github.com/baidubce/bce-sdk-go/http"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// The signature is the one the SDK's signer makes of the same request.
func TestSignAsTheSDK(t *testing.T) {
	now := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	body := []byte(`{"httpsEnable":"ON"}`)
	for _, c := range []struct{ method, path, query string }{
		{http.MethodPut, "/v2/www.example.com/certificates", ""},
		{http.MethodGet, "/v2/domain", "marker=a b/c&x="},
	} {
		req, err := http.NewRequest(c.method, "https://cdn.baidubce.com"+c.path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		sdk := &bcehttp.Request{}
		sdk.SetMethod(c.method)
		sdk.SetUri(c.path)
		if c.query != "" {
			q := req.URL.Query()
			for pair := range strings.SplitSeq(c.query, "&") {
				k, v, _ := strings.Cut(pair, "=")
				q.Set(k, v)
				sdk.SetParam(k, v)
			}
			req.URL.RawQuery = q.Encode()
		}
		req.Header.Set("Content-Type", "application/json;charset=utf-8")
		sign(req, "AKID", "SKEY", body, now)

		for k, v := range map[string]string{"Host": "cdn.baidubce.com", "Content-Type": "application/json;charset=utf-8",
			"Content-Length": strconv.Itoa(len(body)), "x-bce-date": "2026-10-19T08:00:00Z"} {
			sdk.SetHeader(k, v)
		}
		cred, err := bceauth.NewBceCredentials("AKID", "SKEY")
		if err != nil {
			t.Fatal(err)
		}
		(&bceauth.BceV1Signer{}).Sign(sdk, cred, &bceauth.SignOptions{HeadersToSign: bceauth.DEFAULT_HEADERS_TO_SIGN, Timestamp: now.Unix(), ExpireSeconds: expireSeconds})
		if got, want := req.Header.Get("Authorization"), sdk.Header("Authorization"); got != want {
			t.Errorf("%s %s:\n got %s\nwant %s", c.method, c.path, got, want)
		}
	}
}

// fakeAPI is the CDN API of an account: the certificates on its
// accelerated domains, by name.
type fakeAPI struct {
	t       *testing.T
	mu      sync.Mutex
	domains map[string]string
	calls   []string
	failing int // a status every call answers, if set
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "bce-auth-v1/AKID/") || r.Header.Get("X-Bce-Date") == "" {
		f.t.Errorf("unsigned: %q", auth)
	}
	fail := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": "no", "requestId": "r"})
	}
	if f.failing != 0 {
		fail(f.failing, map[int]string{403: "AccessDenied", 429: "TooManyRequests", 500: "InternalError"}[f.failing])
		return
	}
	if r.URL.Path == "/v2/domain" {
		var list []map[string]string
		for d := range f.domains {
			list = append(list, map[string]string{"name": d})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"domains": list, "isTruncated": false})
		return
	}
	domain := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/"), "/certificates")
	name, ok := f.domains[domain]
	switch {
	case !ok:
		fail(http.StatusNotFound, "NoSuchDomain")
	case r.Method == http.MethodGet && name == "":
		fail(http.StatusNotFound, "NoSuchCertificate")
	case r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]string{"certId": "cert-" + name, "certName": name})
	case r.Method == http.MethodPut:
		var body struct {
			HTTPSEnable string            `json:"httpsEnable"`
			Certificate map[string]string `json:"certificate"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		if _, err := cryptox.ParseBundle([]byte(body.Certificate["certServerData"]), []byte(body.Certificate["certPrivateData"])); err != nil || body.HTTPSEnable != "ON" {
			f.t.Errorf("put %s: %v", data, err)
		}
		f.domains[domain] = body.Certificate["certName"]
		_ = json.NewEncoder(w).Encode(map[string]string{"certId": "cert-" + body.Certificate["certName"]})
	}
}

func open(t *testing.T, f *fakeAPI, config map[string]string) *cdn {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	m := &Module{client: srv.Client(), base: srv.URL}
	p, err := m.cdnFactory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"access_key": "AKID", "secret_key": "SKEY"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*cdn)
}

func request(t *testing.T, names ...string) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), names...)
	return contracts.DeployRequest{CertificateID: 4, Domains: names, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func problemKey(err error) i18n.Key {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p.Key
	}
	return ""
}

func TestDeploy(t *testing.T) {
	f := &fakeAPI{t: t, domains: map[string]string{"example.com": "", "www.example.com": "old", "img.example.com": ""}}
	c := open(t, f, map[string]string{})
	req := request(t, "*.example.com", "example.com", "www.example.com")
	var keys []i18n.Key
	req.Log = func(l contracts.LogLine) { keys = append(keys, l.Key) }

	res, err := c.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	name := "jec-4-" + cryptox.Fingerprint(req.Chain[0])[:12]
	if f.domains["example.com"] != name || f.domains["www.example.com"] != name || f.domains["img.example.com"] != "" || res.Ref != "cert-"+name+",cert-"+name {
		t.Errorf("domains %v, ref %s", f.domains, res.Ref)
	}
	if !slices.Equal(keys, []i18n.Key{i18n.BaiducloudLogSet, i18n.BaiducloudLogSet, i18n.BaiducloudLogDeployed}) {
		t.Errorf("log %v", keys)
	}
	// Again, as after an interrupted attempt: nothing is set twice.
	f.calls = nil
	if _, err := c.Deploy(t.Context(), req); err != nil || slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "PUT") }) {
		t.Errorf("again: %v, calls %v", err, f.calls)
	}

	// The domains set, one unknown.
	_, err = open(t, f, map[string]string{"domains": "img.example.com, nope.example.com"}).Deploy(t.Context(), req)
	if problemKey(err) != i18n.BaiducloudErrorDomain || !retry.IsPermanent(err) || f.domains["img.example.com"] != name {
		t.Errorf("unknown domain: %v, domains %v", err, f.domains)
	}
}

func TestDeployErrors(t *testing.T) {
	f := &fakeAPI{t: t, domains: map[string]string{"example.com": ""}}
	if _, err := open(t, f, map[string]string{}).Deploy(t.Context(), request(t, "*.example.com")); problemKey(err) != i18n.BaiducloudErrorNoDomains || !retry.IsPermanent(err) {
		t.Errorf("only a wildcard: %v", err)
	}
	for status, want := range map[int]struct {
		key       i18n.Key
		permanent bool
	}{
		http.StatusForbidden:           {i18n.BaiducloudErrorAuth, true},
		http.StatusTooManyRequests:     {i18n.BaiducloudErrorThrottled, false},
		http.StatusInternalServerError: {i18n.BaiducloudErrorApi, false},
	} {
		f.failing = status
		_, err := open(t, f, map[string]string{}).Deploy(t.Context(), request(t, "example.com"))
		if problemKey(err) != want.key || retry.IsPermanent(err) != want.permanent {
			t.Errorf("%d: %v", status, err)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c := &cdn{client: srv.Client(), base: srv.URL, now: time.Now}
	if _, err := c.Test(t.Context()); problemKey(err) != i18n.BaiducloudErrorNetwork || retry.IsPermanent(err) {
		t.Errorf("closed: %v", err)
	}
}

func TestTest(t *testing.T) {
	f := &fakeAPI{t: t, domains: map[string]string{"a.example.com": "", "b.example.com": ""}}
	msg, err := open(t, f, map[string]string{}).Test(t.Context())
	if err != nil || msg.Key != i18n.BaiducloudTestCdnOk || msg.Params["count"] != 2 {
		t.Errorf("test: %+v %v", msg, err)
	}
}

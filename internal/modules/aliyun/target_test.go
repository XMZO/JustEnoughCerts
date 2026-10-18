package aliyun

import (
	"context"
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

	"github.com/alibabacloud-go/tea/dara"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/aliyunapi"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeAPI is a CDN with some accelerated domains.
type fakeAPI struct {
	domains []string
	set     map[string]string // the chain each domain got
	fail    error
}

func (f *fakeAPI) setCertificate(_ context.Context, domain, chain, key string) error {
	if f.fail != nil {
		return f.fail
	}
	if !slices.Contains(f.domains, domain) {
		return &dara.SDKError{Code: dara.String("InvalidDomain.NotFound"), StatusCode: dara.Int(404)}
	}
	if !strings.Contains(key, "PRIVATE KEY") {
		return errors.New("no key")
	}
	f.set[domain] = chain
	return nil
}

func (f *fakeAPI) check(_ context.Context, domain string) error {
	if !slices.Contains(f.domains, domain) {
		return &dara.SDKError{Code: dara.String("InvalidDomain.NotFound"), StatusCode: dara.Int(404)}
	}
	return f.fail
}

func open(t *testing.T, api *fakeAPI, svc service, config map[string]string) *target {
	t.Helper()
	m := &Module{open: func(s service, id, secret string) (cdnAPI, error) {
		if s.id != svc.id || id != "LTAI-id" || secret != "s3cret" {
			t.Errorf("opened %s with %s %s", s.id, id, secret)
		}
		return api, nil
	}}
	p, err := m.factory(svc)(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"access_key_id": "LTAI-id", "access_key_secret": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*target)
}

func request(t *testing.T, domains ...string) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), domains...)
	return contracts.DeployRequest{CertificateID: 1, Domains: domains, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func TestDeploy(t *testing.T) {
	api := &fakeAPI{domains: []string{"example.com", "www.example.com", "cdn.example.com"}, set: map[string]string{}}
	req := request(t, "*.example.com", "example.com")
	var logged []string
	req.Log = func(l contracts.LogLine) { logged = append(logged, l.Params["domain"].(string)) }

	// The certificate's own domains, but for wildcards.
	if _, err := open(t, api, cdn, nil).Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if len(api.set) != 1 || !slices.Equal(logged, []string{"example.com"}) {
		t.Errorf("set %v, log %v", api.set, logged)
	}
	certs, err := cryptox.ParseCertificates([]byte(api.set["example.com"]))
	if err != nil || len(certs) != 2 || !certs[0].Equal(req.Chain[0]) {
		t.Errorf("the chain: %v", err)
	}

	// The domains set, separated as people do.
	api.set = map[string]string{}
	if _, err := open(t, api, dcdn, map[string]string{"domains": "www.example.com, CDN.example.com.\n"}).Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if len(api.set) != 2 || api.set["cdn.example.com"] == "" {
		t.Errorf("set %v", api.set)
	}

	// A wildcard only: there is no domain to set it on.
	_, err = open(t, api, cdn, nil).Deploy(t.Context(), request(t, "*.example.com"))
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.AliyunErrorNoDomains || !retry.IsPermanent(err) {
		t.Errorf("only a wildcard: %v", err)
	}
	_, err = open(t, api, cdn, map[string]string{"domains": "missing.example.com"}).Deploy(t.Context(), req)
	if !errors.As(err, &p) || p.Key != i18n.AliyunErrorDomain || p.Params["domain"] != "missing.example.com" || !retry.IsPermanent(err) {
		t.Errorf("a missing domain: %v", err)
	}
}

func TestTest(t *testing.T) {
	api := &fakeAPI{domains: []string{"example.com", "www.example.com"}}
	msg, err := open(t, api, cdn, map[string]string{"domains": "example.com,www.example.com"}).Test(t.Context())
	if err != nil || msg.Key != i18n.AliyunTestOk || msg.Params["domains"] != "example.com, www.example.com" {
		t.Errorf("test %+v %v", msg, err)
	}
	_, err = open(t, api, cdn, nil).Test(t.Context())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.AliyunErrorNoDomains {
		t.Errorf("no domains: %v", err)
	}
	api.fail = &dara.SDKError{Code: dara.String("InvalidAccessKeyId.NotFound"), StatusCode: dara.Int(404)}
	_, err = open(t, api, cdn, map[string]string{"domains": "example.com"}).Test(t.Context())
	if !errors.As(err, &p) || p.Key != i18n.AliyunErrorAuth || !retry.IsPermanent(err) {
		t.Errorf("a wrong key: %v", err)
	}
}

func TestExplain(t *testing.T) {
	for _, c := range []struct {
		err       error
		key       i18n.Key
		permanent bool
	}{
		{&dara.SDKError{Code: dara.String("SignatureDoesNotMatch"), StatusCode: dara.Int(400)}, i18n.AliyunErrorAuth, true},
		{&dara.SDKError{Code: dara.String("Forbidden.RAM"), StatusCode: dara.Int(403)}, i18n.AliyunErrorAuth, true},
		{&dara.SDKError{Code: dara.String("NoPermission"), StatusCode: dara.Int(403)}, i18n.AliyunErrorAuth, true},
		{&dara.SDKError{Code: dara.String("InvalidDomain.NotFound"), StatusCode: dara.Int(404)}, i18n.AliyunErrorDomain, true},
		{&dara.SDKError{Code: dara.String("Throttling.User"), StatusCode: dara.Int(400)}, i18n.AliyunErrorThrottled, false},
		{&dara.SDKError{Code: dara.String("InvalidSSLPub.Malformed"), StatusCode: dara.Int(400)}, i18n.AliyunErrorApi, true},
		{&dara.SDKError{Code: dara.String("ServiceUnavailable"), StatusCode: dara.Int(503)}, i18n.AliyunErrorApi, false},
		{errors.New("dial tcp: i/o timeout"), i18n.AliyunErrorNetwork, false},
	} {
		err := explain(c.err, cdn, "example.com")
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%v: %v", c.err, err)
		}
	}
}

// The requests reach Alibaba Cloud's API as its SDKs make them, and its
// errors come back with their codes.
func TestSDK(t *testing.T) {
	var mu sync.Mutex
	var got []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		// The signature of version 3 names the action in headers.
		r.Form.Set("Action", r.Header.Get("x-acs-action"))
		r.Form.Set("Version", r.Header.Get("x-acs-version"))
		mu.Lock()
		got = append(got, r.Form)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("DomainName") == "missing.example.com" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"RequestId":"r2","Code":"InvalidDomain.NotFound","Message":"The domain provided does not belong to you."}`)
			return
		}
		_, _ = io.WriteString(w, `{"RequestId":"r1"}`)
	}))
	defer srv.Close()
	api, err := dial(dcdn, "LTAI-id", "s3cret", strings.TrimPrefix(srv.URL, "http://"), "HTTP")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.setCertificate(t.Context(), "www.example.com", "CHAIN PEM", "KEY PEM"); err != nil {
		t.Fatal(err)
	}
	if err := api.check(t.Context(), "www.example.com"); err != nil {
		t.Fatal(err)
	}
	set, check := got[0], got[1]
	for k, want := range map[string]string{
		"Action": "SetDcdnDomainSSLCertificate", "Version": "2018-01-15", "DomainName": "www.example.com",
		"SSLProtocol": "on", "CertType": "upload", "SSLPub": "CHAIN PEM", "SSLPri": "KEY PEM",
	} {
		if set.Get(k) != want {
			t.Errorf("%s: %q, want %q", k, set.Get(k), want)
		}
	}
	if check.Get("Action") != "DescribeDcdnDomainCertificateInfo" || check.Get("DomainName") != "www.example.com" {
		t.Errorf("check %v", check)
	}
	err = api.check(t.Context(), "missing.example.com")
	if e := explain(err, dcdn, "missing.example.com"); !retry.IsPermanent(e) || !strings.Contains(e.Error(), "aliyun.error.domain") {
		t.Errorf("explained %v", e)
	}
	if code, status := aliyunapi.ErrorCode(err); code != "InvalidDomain.NotFound" || status != 404 {
		t.Errorf("the error: %q %d %v", code, status, err)
	}
}

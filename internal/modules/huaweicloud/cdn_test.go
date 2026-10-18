package huaweicloud

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeCDN is Huawei Cloud's CDN with some accelerated domains: the HTTPS
// settings of each, as the API shows them.
type fakeCDN struct {
	mu      sync.Mutex
	https   map[string]map[string]any
	puts    []map[string]any // the https of each update
	headers []http.Header
	status  int // answers every request with this status, unless 0
}

func (f *fakeCDN) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.headers = append(f.headers, r.Header.Clone())
	w.Header().Set("Content-Type", "application/json")
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, `{"error":{"error_code":"APIGW.0301","error_msg":"Incorrect IAM authentication information"}}`)
		return
	}
	domain, ok := strings.CutPrefix(r.URL.Path, "/v1.1/cdn/configuration/domains/")
	domain, ok2 := strings.CutSuffix(domain, "/configs")
	https, known := f.https[domain]
	if !ok || !ok2 || !known {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"error_code":"CDN.0170","error_msg":"domain not found"}}`)
		return
	}
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{"configs": map[string]any{"https": https}})
	case http.MethodPut:
		var body struct {
			Configs struct {
				HTTPS map[string]any `json:"https"`
			} `json:"configs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.puts = append(f.puts, body.Configs.HTTPS)
		f.https[domain] = body.Configs.HTTPS
		_, _ = io.WriteString(w, `{}`)
	}
}

func open(t *testing.T, f *fakeCDN, config map[string]string) *cdn {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := map[string]string{"site": siteChina, "domain_id": "d-1"}
	for k, v := range config {
		cfg[k] = v
	}
	p, err := (&Module{endpoint: srv.URL}).factory(t.Context(), providers.Instance{Config: cfg,
		Secrets: map[string]string{"access_key_id": "AK1", "secret_access_key": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*cdn)
}

func request(t *testing.T, domains ...string) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), domains...)
	return contracts.DeployRequest{CertificateID: 7, Domains: domains, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func TestDeploy(t *testing.T) {
	f := &fakeCDN{https: map[string]map[string]any{
		"example.com":     {"https_status": "on", "http2_status": "on", "tls_version": "TLSv1.2,TLSv1.3", "ocsp_stapling_status": "on"},
		"www.example.com": {"https_status": "off"},
	}}
	c := open(t, f, nil)
	req := request(t, "example.com", "www.example.com", "*.example.com")
	if _, err := c.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if len(f.puts) != 2 {
		t.Fatalf("%d updates", len(f.puts))
	}
	first, second := f.puts[0], f.puts[1]
	// The settings the domain had stay; a domain without HTTPS gets it.
	if first["https_status"] != "on" || first["http2_status"] != "on" || first["tls_version"] != "TLSv1.2,TLSv1.3" || first["ocsp_stapling_status"] != "on" ||
		first["certificate_type"] != "server" || first["certificate_source"] != float64(0) || !strings.HasPrefix(first["certificate_name"].(string), "jec-7-") ||
		strings.Count(first["certificate_value"].(string), "BEGIN CERTIFICATE") != 2 || !strings.Contains(first["private_key"].(string), "PRIVATE KEY") {
		t.Errorf("first update %v", first)
	}
	if second["https_status"] != "on" || second["http2_status"] != nil {
		t.Errorf("second update %v", second)
	}
	// Signed with the AK, for the account given.
	if h := f.headers[0]; !strings.Contains(h.Get("Authorization"), "Access=AK1") || h.Get("X-Domain-Id") != "d-1" {
		t.Errorf("headers %v", h)
	}
}

func TestDomainsSet(t *testing.T) {
	f := &fakeCDN{https: map[string]map[string]any{"cdn.example.com": {}}}
	c := open(t, f, map[string]string{"domains": "CDN.example.com."})
	if _, err := c.Deploy(t.Context(), request(t, "example.com")); err != nil || len(f.puts) != 1 {
		t.Errorf("deploy: %v, %d updates", err, len(f.puts))
	}
	msg, err := c.Test(t.Context())
	if err != nil || msg.Key != i18n.HuaweicloudTestOk || msg.Params["domains"] != "cdn.example.com" {
		t.Errorf("test: %+v, %v", msg, err)
	}
}

func TestErrors(t *testing.T) {
	var p *contracts.Problem
	f := &fakeCDN{https: map[string]map[string]any{}}
	c := open(t, f, nil)
	if _, err := c.Deploy(t.Context(), request(t, "missing.example.com")); !errors.As(err, &p) || p.Key != i18n.HuaweicloudErrorDomain || !retry.IsPermanent(err) {
		t.Errorf("a domain the account lacks: %v", err)
	}
	if _, err := c.Deploy(t.Context(), request(t, "*.example.com")); !errors.As(err, &p) || p.Key != i18n.HuaweicloudErrorNoDomains {
		t.Errorf("only wildcards: %v", err)
	}
	if _, err := c.Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.HuaweicloudErrorNoDomains {
		t.Errorf("a test without domains: %v", err)
	}
	f.status = http.StatusUnauthorized
	if _, err := c.Deploy(t.Context(), request(t, "example.com")); !errors.As(err, &p) || p.Key != i18n.HuaweicloudErrorAuth || p.Params["code"] != "APIGW.0301" || !retry.IsPermanent(err) {
		t.Errorf("a wrong key: %v", err)
	}
}

func TestExplain(t *testing.T) {
	for _, c := range []struct {
		err       error
		key       i18n.Key
		permanent bool
	}{
		{&sdkerr.ServiceResponseError{StatusCode: 403, ErrorCode: "CDN.0001"}, i18n.HuaweicloudErrorAuth, true},
		{&sdkerr.ServiceResponseError{StatusCode: 429, ErrorCode: "APIGW.0308"}, i18n.HuaweicloudErrorThrottled, false},
		{&sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "CDN.0100", ErrorMessage: "bad certificate"}, i18n.HuaweicloudErrorApi, true},
		{&sdkerr.ServiceResponseError{StatusCode: 502, ErrorCode: "APIGW.0201"}, i18n.HuaweicloudErrorApi, false},
		{errors.New("dial tcp: i/o timeout"), i18n.HuaweicloudErrorNetwork, false},
	} {
		err := explain(c.err, "example.com")
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%v: %v", c.err, err)
		}
	}
}

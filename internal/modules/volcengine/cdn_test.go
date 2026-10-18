package volcengine

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

// fakeAPI is the CDN API of an account: its certificate center and
// accelerated domains. It checks that each request is signed.
type fakeAPI struct {
	t       *testing.T
	mu      sync.Mutex
	certs   map[string]string // the fingerprints, by ID
	domains map[string]string // the certificates on the domains
	calls   []string
	failing string // the code every call answers, if set
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	action := r.URL.Query().Get("Action")
	f.calls = append(f.calls, action)
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "HMAC-SHA256 Credential=AKLT-id/") || !strings.Contains(auth, "/cn-north-1/CDN/request, SignedHeaders=") ||
		r.Header.Get("X-Date") == "" || r.Header.Get("X-Content-Sha256") == "" || r.URL.Query().Get("Version") != cdnVersion {
		f.t.Errorf("%s: unsigned: %q %v", action, auth, r.Header)
	}
	var params map[string]any
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &params); err != nil {
		f.t.Errorf("%s: body %q", action, body)
	}
	answer := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ResponseMetadata": map[string]any{"RequestId": "r", "Action": action}, "Result": result})
	}
	if f.failing != "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"ResponseMetadata": map[string]any{"Error": map[string]any{"Code": f.failing, "Message": "no"}}})
		return
	}
	switch action {
	case "ListCertInfo":
		var list []map[string]any
		for id, fp := range f.certs {
			list = append(list, map[string]any{"CertId": id, "CertFingerprint": map[string]any{"Sha256": fp}})
		}
		answer(map[string]any{"Total": len(list), "CertInfo": list})
	case "AddCertificate":
		chain, err := cryptox.ParseCertificates([]byte(params["Certificate"].(string)))
		if _, kerr := cryptox.ParseKey([]byte(params["PrivateKey"].(string))); err != nil || kerr != nil || params["Source"] != certCenter {
			f.t.Errorf("add %v", params)
		}
		id := "cert-" + params["Desc"].(string)
		f.certs[id] = strings.ToUpper(cryptox.Fingerprint(chain[0]))
		answer(map[string]any{"CertId": id})
	case "BatchDeployCert":
		var results []map[string]any
		for d := range strings.SplitSeq(params["Domain"].(string), ",") {
			if _, ok := f.domains[d]; !ok {
				results = append(results, map[string]any{"Domain": d, "Status": "failed", "ErrorMsg": "domain not found"})
				continue
			}
			f.domains[d] = params["CertId"].(string)
			results = append(results, map[string]any{"Domain": d, "Status": "success"})
		}
		answer(map[string]any{"DeployResult": results})
	case "ListCdnDomains":
		answer(map[string]any{"Total": len(f.domains), "Data": []any{}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func open(t *testing.T, f *fakeAPI, config map[string]string) *cdn {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	m := &Module{client: srv.Client(), base: srv.URL}
	p, err := m.cdnFactory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"access_key_id": "AKLT-id", "secret_access_key": "s3cret"}})
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
	f := &fakeAPI{t: t, certs: map[string]string{}, domains: map[string]string{"example.com": "", "www.example.com": "", "img.example.com": ""}}
	c := open(t, f, map[string]string{})
	req := request(t, "*.example.com", "example.com", "www.example.com")
	var keys []i18n.Key
	req.Log = func(l contracts.LogLine) { keys = append(keys, l.Key) }

	// Uploaded once, deployed to the certificate's domains but the
	// wildcard.
	res, err := c.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.certs) != 1 || f.domains["example.com"] != res.Ref || f.domains["www.example.com"] != res.Ref || f.domains["img.example.com"] != "" {
		t.Errorf("certs %v, domains %v, ref %s", f.certs, f.domains, res.Ref)
	}
	if !slices.Equal(keys, []i18n.Key{i18n.VolcengineLogUpload, i18n.VolcengineLogDeploy, i18n.VolcengineLogDeployed}) {
		t.Errorf("log %v", keys)
	}
	// Again, as after an interrupted attempt: no second upload.
	if res2, err := c.Deploy(t.Context(), req); err != nil || res2.Ref != res.Ref || len(f.certs) != 1 {
		t.Errorf("again: %+v %v, certs %v", res2, err, f.certs)
	}

	// The domains set, one unknown.
	c = open(t, f, map[string]string{"domains": "img.example.com, nope.example.com"})
	_, err = c.Deploy(t.Context(), req)
	if problemKey(err) != i18n.VolcengineErrorDeploy || retry.IsPermanent(err) || f.domains["img.example.com"] != res.Ref {
		t.Errorf("unknown domain: %v, domains %v", err, f.domains)
	}
}

func TestDeployErrors(t *testing.T) {
	f := &fakeAPI{t: t, certs: map[string]string{}, domains: map[string]string{}}
	if _, err := open(t, f, map[string]string{}).Deploy(t.Context(), request(t, "*.example.com")); problemKey(err) != i18n.VolcengineErrorNoDomains || !retry.IsPermanent(err) {
		t.Errorf("only a wildcard: %v", err)
	}
	for code, want := range map[string]struct {
		key       i18n.Key
		permanent bool
	}{
		"InvalidAccessKey":      {i18n.VolcengineErrorAuth, true},
		"SignatureDoesNotMatch": {i18n.VolcengineErrorAuth, true},
		"FlowLimitExceeded":     {i18n.VolcengineErrorThrottled, false},
		"InvalidParameter":      {i18n.VolcengineErrorApi, true},
	} {
		f.failing = code
		_, err := open(t, f, map[string]string{"domains": "example.com"}).Deploy(t.Context(), request(t, "example.com"))
		if problemKey(err) != want.key || retry.IsPermanent(err) != want.permanent {
			t.Errorf("%s: %v", code, err)
		}
	}
	// Nothing answers.
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c := &cdn{client: srv.Client(), base: srv.URL}
	if _, err := c.Test(t.Context()); problemKey(err) != i18n.VolcengineErrorNetwork || retry.IsPermanent(err) {
		t.Errorf("closed: %v", err)
	}
}

func TestTest(t *testing.T) {
	f := &fakeAPI{t: t, domains: map[string]string{"a.example.com": "", "b.example.com": ""}}
	msg, err := open(t, f, map[string]string{}).Test(t.Context())
	if err != nil || msg.Key != i18n.VolcengineTestCdnOk || msg.Params["count"] != 2 {
		t.Errorf("test: %+v %v", msg, err)
	}
}

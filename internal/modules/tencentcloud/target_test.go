package tencentcloud

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeAPI is the SSL Certificates service of an account.
type fakeAPI struct {
	certs   map[string]string // the chains, by ID
	updates []string          // old>new, with the types
	pending int               // how many times update answers 0 first
	removed []string
	fail    error
	busy    []string // certificates still deployed, which cannot go
	// listed are the certificates Tencent Cloud issued, and archives
	// their downloads by ID.
	listed   []issuedCert
	archives map[string][]byte
	// applied are the free certificates applied for, by ID, with their
	// statuses; completed those the CA was asked to check now.
	applied    map[string]*certStatus
	appliedAs  []string // domain and algorithm of each
	completed  []string
	applyFail  error
	statusFail error
	// deployed are the deployments to resources, and bound the hosts of
	// EdgeOne zones given certificates.
	deployed []string
	bound    []string
}

func (f *fakeAPI) deploy(_ context.Context, id, resource string, instances []string) (int64, error) {
	if f.fail != nil {
		return 0, f.fail
	}
	f.deployed = append(f.deployed, resource+":"+strings.Join(instances, ",")+">"+id)
	return 77, nil
}

func (f *fakeAPI) bindHosts(_ context.Context, zone string, hosts []string, id string) error {
	if f.fail != nil {
		return f.fail
	}
	f.bound = append(f.bound, zone+":"+strings.Join(hosts, ",")+">"+id)
	return nil
}

func (f *fakeAPI) upload(_ context.Context, alias, chain, key string) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	if !strings.Contains(key, "PRIVATE KEY") {
		return "", errors.New("no key")
	}
	for id, c := range f.certs {
		if c == chain {
			return id, nil
		}
	}
	id := "cert-" + alias
	f.certs[id] = chain
	return id, nil
}

func (f *fakeAPI) update(_ context.Context, old, id string, types, _ []string) (int64, error) {
	if _, ok := f.certs[old]; !ok {
		return 0, &tcerr.TencentCloudSDKError{Code: "FailedOperation.CertificateNotFound"}
	}
	if f.pending > 0 {
		f.pending--
		return 0, nil
	}
	f.updates = append(f.updates, old+">"+id+" "+strings.Join(types, ","))
	return 1763489, nil
}

func (f *fakeAPI) remove(_ context.Context, id string) error {
	if slices.Contains(f.busy, id) {
		return &tcerr.TencentCloudSDKError{Code: "FailedOperation.DeleteResourceFailed"}
	}
	delete(f.certs, id)
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeAPI) count(context.Context) (int, error) {
	if f.fail != nil {
		return 0, f.fail
	}
	return len(f.certs), nil
}

func (f *fakeAPI) issued(context.Context, string) ([]issuedCert, error) {
	return f.listed, f.fail
}

func (f *fakeAPI) download(_ context.Context, id string) ([]byte, error) {
	data, ok := f.archives[id]
	if !ok {
		return nil, &tcerr.TencentCloudSDKError{Code: "FailedOperation.CertificateNotFound"}
	}
	return data, nil
}

func (f *fakeAPI) apply(_ context.Context, domain, algo string) (string, error) {
	if f.applyFail != nil {
		return "", f.applyFail
	}
	id := "free-" + strconv.Itoa(len(f.applied)+1)
	f.applied[id] = &certStatus{Status: statusReviewing, Name: "reviewing"}
	f.appliedAs = append(f.appliedAs, domain+" "+algo)
	return id, nil
}

func (f *fakeAPI) status(_ context.Context, id string) (certStatus, error) {
	if f.statusFail != nil {
		return certStatus{}, f.statusFail
	}
	st, ok := f.applied[id]
	if !ok {
		return certStatus{}, &tcerr.TencentCloudSDKError{Code: "FailedOperation.CertificateNotFound"}
	}
	return *st, nil
}

func (f *fakeAPI) complete(_ context.Context, id string) error {
	f.completed = append(f.completed, id)
	return nil
}

func open(t *testing.T, api *fakeAPI, config map[string]string) *target {
	t.Helper()
	m := &Module{open: func(id, key, endpoint string) (sslAPI, error) {
		if id != "AKID-id" || key != "s3cret" || endpoint != endpointChina {
			t.Errorf("opened %s %s %s", id, key, endpoint)
		}
		return api, nil
	}}
	cfg := map[string]string{"resources": "cdn, TEO", "delete_old": "true"}
	for k, v := range config {
		cfg[k] = v
	}
	p, err := m.factory(t.Context(), providers.Instance{Config: cfg, Secrets: map[string]string{"secret_id": "AKID-id", "secret_key": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*target)
}

func request(t *testing.T) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com")
	return contracts.DeployRequest{CertificateID: 4, Domains: []string{"example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func TestDeploy(t *testing.T) {
	old := updatePoll
	updatePoll = time.Millisecond
	t.Cleanup(func() { updatePoll = old })
	api := &fakeAPI{certs: map[string]string{"console-cert": "CHAIN"}, pending: 2}

	// The first version replaces the certificate set to replace.
	req := request(t)
	var levels []contracts.Level
	req.Log = func(l contracts.LogLine) { levels = append(levels, l.Level) }
	res, err := open(t, api, map[string]string{"replace": "console-cert"}).Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	certs, err := cryptox.ParseCertificates([]byte(api.certs[res.Ref]))
	if err != nil || len(certs) != 2 || !certs[0].Equal(req.Chain[0]) {
		t.Errorf("uploaded %s: %v", res.Ref, err)
	}
	if !slices.Equal(api.updates, []string{"console-cert>" + res.Ref + " cdn,teo"}) || len(res.Removed) != 0 || api.pending != 0 {
		t.Errorf("updates %v, removed %v", api.updates, res.Removed)
	}
	first := res.Ref

	// The next one replaces it; the one before goes, but for the one
	// being replaced.
	api.certs["cert-older"] = "OLDER"
	next := request(t)
	res, err = open(t, api, map[string]string{"replace": "console-cert"}).Deploy(t.Context(), contracts.DeployRequest{
		CertificateID: 4, Domains: next.Domains, Chain: next.Chain, Key: next.Key, Previous: []string{first, "cert-older"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if api.updates[1] != first+">"+res.Ref+" cdn,teo" || !slices.Equal(res.Removed, []string{"cert-older"}) || api.certs[first] == "" {
		t.Errorf("updates %v, removed %v", api.updates, res.Removed)
	}

	// The same version again, as after a deployment tried again: nothing
	// to replace.
	again, err := open(t, api, nil).Deploy(t.Context(), contracts.DeployRequest{
		CertificateID: 4, Domains: next.Domains, Chain: next.Chain, Key: next.Key, Previous: []string{res.Ref, first},
	})
	if err != nil || again.Ref != res.Ref || len(api.updates) != 2 || !slices.Equal(again.Removed, []string{first}) {
		t.Errorf("again %+v %v, updates %v", again, err, api.updates)
	}
	_ = levels
}

func TestDeployWithoutOld(t *testing.T) {
	api := &fakeAPI{certs: map[string]string{}}
	req := request(t)
	var warned bool
	req.Log = func(l contracts.LogLine) {
		if l.Key == i18n.TencentcloudLogNoOld {
			warned = l.Level == contracts.Warn
		}
	}
	res, err := open(t, api, nil).Deploy(t.Context(), req)
	if err != nil || res.Ref == "" || len(api.updates) != 0 || !warned {
		t.Errorf("deploy %+v %v, warned %v", res, err, warned)
	}
}

func TestDeployErrors(t *testing.T) {
	old, tries := updatePoll, updateTries
	updatePoll, updateTries = time.Millisecond, 3
	t.Cleanup(func() { updatePoll, updateTries = old, tries })
	for _, c := range []struct {
		api       *fakeAPI
		config    map[string]string
		key       i18n.Key
		permanent bool
	}{
		{&fakeAPI{certs: map[string]string{}}, map[string]string{"replace": "missing"}, i18n.TencentcloudErrorApi, true},
		{&fakeAPI{certs: map[string]string{"x": "X"}, pending: 10}, map[string]string{"replace": "x"}, i18n.TencentcloudErrorUpdateSlow, false},
		{&fakeAPI{certs: map[string]string{}, fail: &tcerr.TencentCloudSDKError{Code: "AuthFailure.SecretIdNotFound"}}, nil, i18n.TencentcloudErrorAuth, true},
		{&fakeAPI{certs: map[string]string{}, fail: &tcerr.TencentCloudSDKError{Code: "RequestLimitExceeded"}}, nil, i18n.TencentcloudErrorThrottled, false},
		{&fakeAPI{certs: map[string]string{}}, map[string]string{"resources": "cdn, mysql"}, i18n.TencentcloudErrorResource, true},
		{&fakeAPI{certs: map[string]string{}}, map[string]string{"resources": " "}, i18n.TencentcloudErrorResources, true},
	} {
		_, err := open(t, c.api, c.config).Deploy(t.Context(), request(t))
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%v: %v", c.config, err)
		}
	}
}

func TestTest(t *testing.T) {
	api := &fakeAPI{certs: map[string]string{"a": "A", "b": "B"}}
	msg, err := open(t, api, nil).Test(t.Context())
	if err != nil || msg.Key != i18n.TencentcloudTestOk || msg.Params["count"] != 2 {
		t.Errorf("test %+v %v", msg, err)
	}
}

// The requests reach Tencent Cloud's API as its SDK makes them, and its
// errors come back with their codes.
func TestSDK(t *testing.T) {
	var got []map[string]any
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.Header.Get("X-TC-Action")
		actions = append(actions, action+" "+r.Header.Get("X-TC-Version"))
		var params map[string]any
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &params); err != nil {
			t.Error(err)
		}
		got = append(got, params)
		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "UploadCertificate":
			_, _ = io.WriteString(w, `{"Response":{"CertificateId":"","RepeatCertId":"same-id","RequestId":"r"}}`)
		case "UpdateCertificateInstance":
			_, _ = io.WriteString(w, `{"Response":{"DeployRecordId":1763489,"DeployStatus":1,"RequestId":"r"}}`)
		case "DescribeCertificates":
			_, _ = io.WriteString(w, `{"Response":{"TotalCount":7,"Certificates":[],"RequestId":"r"}}`)
		case "DeployCertificateInstance":
			_, _ = io.WriteString(w, `{"Response":{"DeployRecordId":88,"DeployStatus":1,"RequestId":"r"}}`)
		case "ModifyHostsCertificate":
			_, _ = io.WriteString(w, `{"Response":{"RequestId":"r"}}`)
		default:
			_, _ = io.WriteString(w, `{"Response":{"Error":{"Code":"FailedOperation.DeleteResourceFailed","Message":"in use"},"RequestId":"r"}}`)
		}
	}))
	defer srv.Close()
	api, err := dial("AKID-id", "s3cret", strings.TrimPrefix(srv.URL, "http://"), "HTTP")
	if err != nil {
		t.Fatal(err)
	}
	if id, err := api.upload(t.Context(), "jec-4-abc", "CHAIN", "KEY"); err != nil || id != "same-id" {
		t.Errorf("upload %q %v", id, err)
	}
	if record, err := api.update(t.Context(), "old", "new", []string{"cdn", "clb"}, []string{"ap-guangzhou"}); err != nil || record != 1763489 {
		t.Errorf("update %d %v", record, err)
	}
	if n, err := api.count(t.Context()); err != nil || n != 7 {
		t.Errorf("count %d %v", n, err)
	}
	err = api.remove(t.Context(), "old")
	if code := errorCode(err); code != "FailedOperation.DeleteResourceFailed" {
		t.Errorf("remove: %q %v", code, err)
	}
	if record, err := api.deploy(t.Context(), "new", "cdn", []string{"example.com"}); err != nil || record != 88 {
		t.Errorf("deploy %d %v", record, err)
	}
	if err := api.bindHosts(t.Context(), "zone-1", []string{"example.com"}, "new"); err != nil {
		t.Errorf("bind hosts: %v", err)
	}
	if !slices.Equal(actions, []string{"UploadCertificate 2019-12-05", "UpdateCertificateInstance 2019-12-05", "DescribeCertificates 2019-12-05",
		"DeleteCertificate 2019-12-05", "DeployCertificateInstance 2019-12-05", "ModifyHostsCertificate 2022-09-01"}) {
		t.Errorf("actions %v", actions)
	}
	if hosts, _ := got[5]["Hosts"].([]any); got[5]["ZoneId"] != "zone-1" || got[5]["Mode"] != "sslcert" || len(hosts) != 1 {
		t.Errorf("bind hosts sent %v", got[5])
	}
	if got[0]["CertificatePublicKey"] != "CHAIN" || got[0]["CertificatePrivateKey"] != "KEY" || got[0]["Repeatable"] != false || got[0]["CertificateType"] != "SVR" {
		t.Errorf("upload sent %v", got[0])
	}
	regions, _ := got[1]["ResourceTypesRegions"].([]any)
	if got[1]["OldCertificateId"] != "old" || got[1]["CertificateId"] != "new" || len(regions) != 1 {
		t.Errorf("update sent %v", got[1])
	}
}

package esa

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alibabacloud-go/tea/dara"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeAPI is a site of ESA in memory.
type fakeAPI struct {
	site     string
	held     []certificate
	calls    []string
	limit    int   // certificates a site holds at most; 0 for no limit
	failNext error // returned by the next upload, once
	failDel  error // returned by every removal
	next     int
}

func (f *fakeAPI) siteID(_ context.Context, name string) (int64, error) {
	f.calls = append(f.calls, "site "+name)
	if name != f.site {
		return 0, errSiteNotFound
	}
	return 7, nil
}

func (f *fakeAPI) certificates(context.Context, int64) ([]certificate, error) {
	return slices.Clone(f.held), nil
}

func (f *fakeAPI) upload(_ context.Context, _ int64, name, chain, key string) (string, error) {
	f.calls = append(f.calls, "upload "+name)
	if err := f.failNext; err != nil {
		f.failNext = nil
		return "", err
	}
	if f.limit > 0 && len(f.held) >= f.limit {
		return "", &dara.SDKError{Code: dara.String("Certificate.ExceedQuota"), StatusCode: dara.Int(400)}
	}
	block, _ := pem.Decode([]byte(chain))
	if block == nil || !strings.Contains(key, "PRIVATE KEY") {
		return "", errors.New("bad PEM")
	}
	sum := sha256.Sum256(block.Bytes)
	f.next++
	id := "cert-" + string(rune('0'+f.next))
	f.held = append(f.held, certificate{ID: id, Name: name, Fingerprint: hex.EncodeToString(sum[:])})
	return id, nil
}

func (f *fakeAPI) remove(_ context.Context, _ int64, id string) error {
	f.calls = append(f.calls, "remove "+id)
	if f.failDel != nil {
		return f.failDel
	}
	f.held = slices.DeleteFunc(f.held, func(c certificate) bool { return c.ID == id })
	return nil
}

func newTarget(api *fakeAPI, deleteOld bool) *target {
	return &target{api: api, site: "example.com", deleteOld: deleteOld}
}

// request deploys a new certificate for example.com, with previous refs.
func request(t *testing.T, previous ...string) (contracts.DeployRequest, *[]i18n.Key) {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour), "example.com")
	var logged []i18n.Key
	return contracts.DeployRequest{
		CertificateID: 12, Domains: []string{"example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
		Previous: previous, Log: func(l contracts.LogLine) { logged = append(logged, l.Key) },
	}, &logged
}

func TestDeploy(t *testing.T) {
	api := &fakeAPI{site: "example.com"}
	req, logged := request(t)
	res, err := newTarget(api, true).Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(req.Chain[0].Raw)
	name := "jec-12-" + hex.EncodeToString(sum[:])[:12]
	if res.Ref != "cert-1" || len(res.Removed) != 0 || !slices.Equal(api.calls, []string{"site example.com", "upload " + name}) {
		t.Errorf("result %+v, calls %v", res, api.calls)
	}
	if !slices.Equal(*logged, []i18n.Key{i18n.EsaLogSite, i18n.EsaLogUpload, i18n.EsaLogUploaded}) {
		t.Errorf("log %v", *logged)
	}
}

func TestDeployReplaces(t *testing.T) {
	api := &fakeAPI{site: "example.com", held: []certificate{{ID: "old-2"}, {ID: "old-1"}, {ID: "other"}}}
	req, _ := request(t, "old-2", "old-1", "gone")
	res, err := newTarget(api, true).Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "cert-1" || !slices.Equal(res.Removed, []string{"old-2", "old-1", "gone"}) {
		t.Errorf("result %+v", res)
	}
	if ids := certIDs(api.held); !slices.Equal(ids, []string{"other", "cert-1"}) {
		t.Errorf("the site holds %v", ids)
	}

	// Kept when the target is set to.
	api = &fakeAPI{site: "example.com", held: []certificate{{ID: "old-1"}}}
	res, err = newTarget(api, false).Deploy(t.Context(), req)
	if err != nil || len(res.Removed) != 0 || len(api.held) != 2 {
		t.Errorf("without removing: %+v, %v, %v", res, err, certIDs(api.held))
	}
}

func TestDeployFindsItsUpload(t *testing.T) {
	api := &fakeAPI{site: "example.com"}
	req, _ := request(t, "old-1")
	if _, err := newTarget(api, true).Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	// Tried again, as after a stop of JEC: the same certificate is there.
	api.calls = nil
	req2, logged := request(t, "old-1")
	req2.Chain, req2.Key = req.Chain, req.Key
	res, err := newTarget(api, true).Deploy(t.Context(), req2)
	if err != nil || res.Ref != "cert-1" || slices.ContainsFunc(api.calls, func(c string) bool { return strings.HasPrefix(c, "upload") }) {
		t.Errorf("result %+v, %v, calls %v", res, err, api.calls)
	}
	if !slices.Contains(*logged, i18n.EsaLogReuse) {
		t.Errorf("log %v", *logged)
	}
}

func TestDeployMakesRoom(t *testing.T) {
	api := &fakeAPI{site: "example.com", limit: 2, held: []certificate{{ID: "old-2"}, {ID: "old-1"}}}
	req, logged := request(t, "old-2", "old-1")
	res, err := newTarget(api, true).Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "cert-1" || !slices.Equal(res.Removed, []string{"old-1", "old-2"}) || len(api.held) != 1 {
		t.Errorf("result %+v, held %v", res, certIDs(api.held))
	}
	if !slices.Contains(*logged, i18n.EsaLogMakeRoom) {
		t.Errorf("log %v", *logged)
	}

	// Without removing old versions, a full site is for a person to sort out.
	api = &fakeAPI{site: "example.com", limit: 1, held: []certificate{{ID: "old-1"}}}
	_, err = newTarget(api, false).Deploy(t.Context(), req)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.EsaErrorQuota || !retry.IsPermanent(err) {
		t.Errorf("a full site: %v", err)
	}
}

func TestDeployErrors(t *testing.T) {
	req, _ := request(t, "old-1")
	for name, tt := range map[string]struct {
		api       *fakeAPI
		key       i18n.Key
		permanent bool
	}{
		"no site":   {&fakeAPI{site: "other.com"}, i18n.EsaErrorSite, true},
		"key":       {&fakeAPI{site: "example.com", failNext: &dara.SDKError{Code: dara.String("InvalidAccessKeyId.NotFound"), StatusCode: dara.Int(404)}}, i18n.EsaErrorAuth, true},
		"forbidden": {&fakeAPI{site: "example.com", failNext: &dara.SDKError{Code: dara.String("Forbidden.RAM"), StatusCode: dara.Int(403)}}, i18n.EsaErrorAuth, true},
		"throttled": {&fakeAPI{site: "example.com", failNext: &dara.SDKError{Code: dara.String("Throttling.User"), StatusCode: dara.Int(400)}}, i18n.EsaErrorThrottled, false},
		"other":     {&fakeAPI{site: "example.com", failNext: &dara.SDKError{Code: dara.String("InvalidCertificate"), StatusCode: dara.Int(400)}}, i18n.EsaErrorApi, false},
	} {
		_, err := newTarget(tt.api, true).Deploy(t.Context(), req)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != tt.key || retry.IsPermanent(err) != tt.permanent {
			t.Errorf("%s: %v (permanent %v)", name, err, retry.IsPermanent(err))
		}
	}

	// A removal that fails leaves the old version for the next deployment.
	api := &fakeAPI{site: "example.com", held: []certificate{{ID: "old-1"}}, failDel: errors.New("timeout")}
	req, logged := request(t, "old-1")
	res, err := newTarget(api, true).Deploy(t.Context(), req)
	if err != nil || res.Ref == "" || len(res.Removed) != 0 || !slices.Contains(*logged, i18n.EsaLogDeleteFailed) {
		t.Errorf("a failed removal: %+v, %v, %v", res, err, *logged)
	}
}

func TestSiteType(t *testing.T) {
	typ := siteType()
	if typ.ID != "esa.site" || typ.Kind != providers.Target || len(typ.Fields) != 5 || !typ.Fields[2].Secret {
		t.Fatalf("type %+v", typ)
	}
	m := &Module{newAPI: func(providers.Instance) (siteAPI, error) { return &fakeAPI{}, nil }}
	p, err := m.factory(t.Context(), providers.Instance{Config: map[string]string{"site": " example.com ", "delete_old": "false"}})
	if err != nil {
		t.Fatal(err)
	}
	if tg := p.(*target); tg.site != "example.com" || tg.deleteOld {
		t.Errorf("target %+v", tg)
	}
	p, _ = m.factory(t.Context(), providers.Instance{Config: map[string]string{"site": "example.com"}})
	if !p.(*target).deleteOld {
		t.Error("old versions are kept by default")
	}
	// The real client takes the region's endpoint.
	api, err := newSDK(providers.Instance{Config: map[string]string{"region": regionGlobal}, Secrets: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := dara.StringValue(api.(*sdk).client.Endpoint); got != "esa.ap-southeast-1.aliyuncs.com" {
		t.Errorf("endpoint %s", got)
	}
}

func certIDs(list []certificate) []string {
	var ids []string
	for _, c := range list {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestTest(t *testing.T) {
	api := &fakeAPI{site: "example.com", held: []certificate{{ID: "a"}, {ID: "b"}}}
	msg, err := newTarget(api, true).Test(t.Context())
	if err != nil || msg.Key != i18n.EsaTestOk || msg.Params["count"] != 2 || msg.Params["site"] != "example.com" {
		t.Errorf("test: %+v, %v", msg, err)
	}
	api.site = "other.example"
	_, err = newTarget(api, true).Test(t.Context())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.EsaErrorSite || !retry.IsPermanent(err) {
		t.Errorf("a site that is not there: %v", err)
	}
}

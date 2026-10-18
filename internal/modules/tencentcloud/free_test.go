package tencentcloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

func openFree(t *testing.T, api *fakeAPI, config map[string]string) *freeCerts {
	t.Helper()
	m := &Module{open: func(id, key, endpoint string) (sslAPI, error) {
		if id != "AKID-id" || key != "s3cret" || endpoint != endpointChina {
			t.Errorf("opened %s %s %s", id, key, endpoint)
		}
		return api, nil
	}}
	p, err := m.freeFactory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"secret_id": "AKID-id", "secret_key": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*freeCerts)
}

// attempt runs Collect for example.com with the state of the last
// attempt, and returns what it found, its Wait, and the keys of its log.
func attempt(t *testing.T, f *freeCerts, state []byte) (*contracts.Collected, *contracts.Wait, []i18n.Key, error) {
	t.Helper()
	var keys []i18n.Key
	got, err := f.Collect(t.Context(), contracts.CollectRequest{Domains: []string{"example.com"}, State: state,
		Log: func(l contracts.LogLine) { keys = append(keys, l.Key) }})
	var w *contracts.Wait
	if errors.As(err, &w) {
		if w.After != freePoll {
			t.Errorf("waits %v", w.After)
		}
		return nil, w, keys, nil
	}
	return got, nil, keys, err
}

func TestFree(t *testing.T) {
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().AddDate(0, 3, 0), "example.com")
	api := &fakeAPI{applied: map[string]*certStatus{}, archives: map[string][]byte{}}
	f := openFree(t, api, map[string]string{"key_algorithm": "ECC"})

	// Applied for, then asked to check now, once.
	_, w, keys, err := attempt(t, f, nil)
	if err != nil || w == nil || !slices.Equal(api.appliedAs, []string{"example.com ECC"}) || !slices.Equal(keys, []i18n.Key{i18n.TencentcloudLogFreeApply, i18n.TencentcloudLogFreeWait}) {
		t.Fatalf("apply: %v %v, log %v", api.appliedAs, err, keys)
	}
	_, w, keys, err = attempt(t, f, w.State)
	if err != nil || w == nil || len(api.appliedAs) != 1 || !slices.Equal(api.completed, []string{"free-1"}) || len(keys) != 0 {
		t.Fatalf("waiting: %v, completed %v, log %v", err, api.completed, keys)
	}

	// Issued: taken in with its key.
	api.applied["free-1"].Status = statusIssued
	api.archives["free-1"] = consoleArchive(t, ca, leaf, cryptoxtest.KeyPEM(t, key))
	got, _, _, err := attempt(t, f, w.State)
	if err != nil || len(got.Chain) != 2 || !got.Chain[0].Equal(leaf) || !key.PublicKey.Equal(got.Key.Public()) {
		t.Fatalf("issued: %v %v", got, err)
	}

	// Refused, or gone: the next issuance applies anew.
	api.applied["free-1"].Status, api.applied["free-1"].Message = statusRefused, "the record was not found"
	_, _, _, err = attempt(t, f, w.State)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.TencentcloudErrorFreeRefused || p.Params["reason"] != "the record was not found" || !contracts.IsRestart(err) || !retry.IsPermanent(err) {
		t.Errorf("refused: %v", err)
	}
	delete(api.applied, "free-1")
	if _, _, _, err := attempt(t, f, w.State); !errors.As(err, &p) || p.Key != i18n.TencentcloudErrorFreeGone || !contracts.IsRestart(err) || retry.IsPermanent(err) {
		t.Errorf("gone: %v", err)
	}
}

func TestFreeFailures(t *testing.T) {
	api := &fakeAPI{applied: map[string]*certStatus{}}
	f := openFree(t, api, map[string]string{})
	for _, domains := range [][]string{{"*.example.com"}, {"example.com", "www.example.com"}} {
		_, err := f.Collect(t.Context(), contracts.CollectRequest{Domains: domains})
		if problemKey(err) != i18n.TencentcloudErrorFreeDomains || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", domains, err)
		}
	}
	api.applyFail = &tcerr.TencentCloudSDKError{Code: "FailedOperation.ExceedsFreeLimit"}
	if _, _, _, err := attempt(t, f, nil); problemKey(err) != i18n.TencentcloudErrorFreeQuota || !retry.IsPermanent(err) {
		t.Errorf("quota: %v", err)
	}
	api.applyFail = &tcerr.TencentCloudSDKError{Code: "AuthFailure.SignatureFailure"}
	if _, _, _, err := attempt(t, f, nil); problemKey(err) != i18n.TencentcloudErrorAuth || !retry.IsPermanent(err) {
		t.Errorf("auth: %v", err)
	}
}

func TestSDKFree(t *testing.T) {
	var got []map[string]any
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var params map[string]any
		_ = json.NewDecoder(r.Body).Decode(&params)
		got, actions = append(got, params), append(actions, r.Header.Get("X-TC-Action"))
		switch r.Header.Get("X-TC-Action") {
		case "ApplyCertificate":
			_, _ = w.Write([]byte(`{"Response":{"CertificateId":"c9","RequestId":"r"}}`))
		case "DescribeCertificate":
			_, _ = w.Write([]byte(`{"Response":{"Status":4,"StatusName":"auto DNS","StatusMsg":"","RequestId":"r"}}`))
		default:
			_, _ = w.Write([]byte(`{"Response":{"RequestId":"r"}}`))
		}
	}))
	defer srv.Close()
	api, err := dial("AKID-id", "s3cret", strings.TrimPrefix(srv.URL, "http://"), "HTTP")
	if err != nil {
		t.Fatal(err)
	}
	if id, err := api.apply(t.Context(), "example.com", "RSA"); err != nil || id != "c9" {
		t.Errorf("apply %q %v", id, err)
	}
	if st, err := api.status(t.Context(), "c9"); err != nil || st.Status != 4 || st.Name != "auto DNS" {
		t.Errorf("status %+v %v", st, err)
	}
	if err := api.complete(t.Context(), "c9"); err != nil {
		t.Error(err)
	}
	if !slices.Equal(actions, []string{"ApplyCertificate", "DescribeCertificate", "CompleteCertificate"}) {
		t.Errorf("actions %v", actions)
	}
	if got[0]["DvAuthMethod"] != "DNS_AUTO" || got[0]["DomainName"] != "example.com" || got[0]["CsrEncryptAlgo"] != "RSA" ||
		got[0]["CsrKeyParameter"] != "2048" || got[0]["DeleteDnsAutoRecord"] != true {
		t.Errorf("apply sent %v", got[0])
	}
}

package aliyun

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeIssued is the Certificate Management Service of an account with
// the certificates it issued, listed as list says, and their details by
// ID.
type fakeIssued struct {
	*fakeRPC
	list    []map[string]any
	details map[string]map[string]string
}

func newFakeIssued() *fakeIssued {
	f := &fakeIssued{details: map[string]map[string]string{}}
	f.fakeRPC = &fakeRPC{answer: func(c rpcCall) (any, error) {
		switch c.action {
		case "ListUserCertificateOrder":
			if c.query["CurrentPage"] != "1" {
				return map[string]any{"CertificateOrderList": []any{}}, nil
			}
			return map[string]any{"TotalCount": len(f.list), "CertificateOrderList": f.list}, nil
		case "GetUserCertificateDetail":
			d, ok := f.details[c.query["CertId"]]
			if !ok {
				return nil, apiError("NotFound", 404)
			}
			return d, nil
		}
		return nil, errors.New("unexpected " + c.action)
	}}
	return f
}

func openFetcher(t *testing.T, f *fakeIssued) *fetcher {
	t.Helper()
	return openRPC(t, f.fakeRPC, func(m *Module) providers.Factory { return m.fetchFactory }, "cas.aliyuncs.com", map[string]string{"site": casChina}).(*fetcher)
}

// collect runs Collect for the domains, with the version in use, and
// returns what it found and the keys of its log.
func collect(t *testing.T, f *fetcher, current string, end time.Time, domains ...string) (*contracts.Collected, []i18n.Key, error) {
	t.Helper()
	var keys []i18n.Key
	got, err := f.Collect(t.Context(), contracts.CollectRequest{Domains: domains, Current: current, CurrentEnd: end,
		Log: func(l contracts.LogLine) { keys = append(keys, l.Key) }})
	return got, keys, err
}

func problemKey(err error) i18n.Key {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p.Key
	}
	return ""
}

func TestFetch(t *testing.T) {
	ca := cryptoxtest.NewCA(t)
	now := time.Now()
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, now.Add(-time.Hour), now.AddDate(0, 3, 0), "example.com", "www.example.com")
	f := newFakeIssued()
	f.list = []map[string]any{
		{"CertificateId": 5, "CommonName": "example.com", "Sans": "example.com,www.example.com", "EndDate": "2099-01-01", "Upload": true},
		{"CertificateId": 6, "CommonName": "example.com", "Sans": "example.com,www.example.com", "EndDate": "2020-01-01", "Expired": true},
		{"CertificateId": 7, "CommonName": "example.com", "Sans": "example.com", "EndDate": "2098-01-01"},
		{"CertificateId": 8, "CommonName": "example.com", "Sans": "example.com,www.example.com", "EndDate": now.AddDate(0, 1, 0).Format(time.DateOnly)},
		{"CertificateId": "9", "CommonName": "example.com", "Sans": "www.example.com", "EndDate": now.AddDate(0, 3, 0).Format(time.DateOnly)},
	}
	f.details["9"] = map[string]string{"Cert": cryptoxtest.CertPEM(leaf, ca.Intermediate), "Key": cryptoxtest.KeyPEM(t, key)}
	s := openFetcher(t, f)

	// The newest certificate the service issued for both domains: not
	// uploaded, not expired, not of one domain only.
	got, keys, err := collect(t, s, "", time.Time{}, "example.com", "www.example.com")
	if err != nil || len(got.Chain) != 2 || !got.Chain[0].Equal(leaf) || !key.PublicKey.Equal(got.Key.Public()) {
		t.Fatalf("collect: %v %v", got, err)
	}
	if !slices.Equal(keys, []i18n.Key{i18n.AliyunLogFetchLook, i18n.AliyunLogFetchFound}) {
		t.Errorf("log %v", keys)
	}
	list := f.called("ListUserCertificateOrder")
	if len(list) != 1 || list[0].query["OrderType"] != "CERT" || list[0].query["Keyword"] != "example.com" || list[0].query["Status"] != "" {
		t.Errorf("list %+v", list)
	}
	if d := f.called("GetUserCertificateDetail"); len(d) != 1 || d[0].query["CertId"] != "9" {
		t.Errorf("detail %+v", d)
	}

	// The one in use, or one older: wait for the next.
	for name, c := range map[string]struct {
		current string
		end     time.Time
	}{
		"same":  {cryptox.Fingerprint(leaf), leaf.NotAfter},
		"older": {"other", leaf.NotAfter.Add(time.Hour)},
	} {
		_, keys, err := collect(t, s, c.current, c.end, "example.com", "www.example.com")
		var w *contracts.Wait
		if !errors.As(err, &w) || w.After != recheck || !slices.Contains(keys, i18n.AliyunLogFetchSame) {
			t.Errorf("%s: %v, log %v", name, err, keys)
		}
	}
	// A reissued certificate, ending when the one in use does, comes in.
	if got, _, err := collect(t, s, "other", leaf.NotAfter, "example.com", "www.example.com"); err != nil || got == nil {
		t.Errorf("reissued: %v", err)
	}

	// Without its intermediates, it comes in with a warning.
	f.details["9"]["Cert"] = cryptoxtest.CertPEM(leaf)
	if _, keys, err := collect(t, s, "", time.Time{}, "example.com", "www.example.com"); err != nil || !slices.Contains(keys, i18n.AliyunLogFetchNoChain) {
		t.Errorf("no chain: %v, log %v", err, keys)
	}
}

func TestFetchFailures(t *testing.T) {
	ca := cryptoxtest.NewCA(t)
	now := time.Now()
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, now.Add(-time.Hour), now.AddDate(0, 3, 0), "example.com")
	f := newFakeIssued()
	f.list = []map[string]any{{"CertificateId": 9, "CommonName": "example.com", "Sans": "example.com", "EndDate": "2099-01-01"}}
	s := openFetcher(t, f)

	if _, _, err := collect(t, s, "", time.Time{}, "other.example.com"); problemKey(err) != i18n.AliyunErrorFetchNone || retry.IsPermanent(err) {
		t.Errorf("none: %v", err)
	}
	f.details["9"] = map[string]string{"Cert": cryptoxtest.CertPEM(leaf, ca.Intermediate)}
	if _, _, err := collect(t, s, "", time.Time{}, "example.com"); problemKey(err) != i18n.AliyunErrorFetchNoKey || !retry.IsPermanent(err) {
		t.Errorf("no key: %v", err)
	}
	f.details["9"]["Key"] = cryptoxtest.KeyPEM(t, cryptoxtest.NewKey(t))
	if _, _, err := collect(t, s, "", time.Time{}, "example.com"); problemKey(err) != i18n.AliyunErrorFetchBad || !retry.IsPermanent(err) {
		t.Errorf("another key: %v", err)
	}
	f.answer = func(rpcCall) (any, error) { return nil, apiError("InvalidAccessKeyId.NotFound", 404) }
	if _, _, err := collect(t, s, "", time.Time{}, "example.com"); problemKey(err) != i18n.AliyunErrorAuth || !retry.IsPermanent(err) {
		t.Errorf("refused key: %v", err)
	}
}

func TestFetchLater(t *testing.T) {
	a := listed{CertificateID: "10", EndDate: "2027-01-01"}
	for i, c := range []struct {
		b    listed
		want bool
	}{
		{listed{CertificateID: "9", EndDate: "2027-01-01"}, true},   // a reissued it, ending the same day
		{listed{CertificateID: "11", EndDate: "2027-01-01"}, false}, // reissued after a
		{listed{CertificateID: "99", EndDate: "2026-12-31"}, true},
		{listed{CertificateID: "1", EndDate: "2027-01-02"}, false},
	} {
		if got := a.later(c.b); got != c.want {
			t.Errorf("case %d: %v", i, got)
		}
	}
}

func TestFetchTest(t *testing.T) {
	f := newFakeIssued()
	f.list = []map[string]any{{"CertificateId": 9}, {"CertificateId": 10}}
	msg, err := openFetcher(t, f).Test(t.Context())
	if err != nil || msg.Key != i18n.AliyunTestFetchOk || msg.Params["count"] != 2 {
		t.Errorf("test: %+v %v", msg, err)
	}
	if c := f.called("ListUserCertificateOrder"); len(c) != 1 || c[0].query["OrderType"] != "CERT" {
		t.Errorf("calls %+v", c)
	}
}

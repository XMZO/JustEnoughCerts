package tencentcloud

import (
	"archive/zip"
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// archive makes a ZIP archive of files, by name.
func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// consoleArchive is the archive the console downloads for leaf, with a
// folder for each kind of web server.
func consoleArchive(t *testing.T, ca *cryptoxtest.CA, leaf *x509.Certificate, key string) []byte {
	t.Helper()
	return archive(t, map[string]string{
		"example.com_nginx/example.com_bundle.crt": cryptoxtest.CertPEM(leaf, ca.Intermediate),
		"example.com_nginx/example.com.key":        key,
		"example.com_apache/2_example.com.crt":     cryptoxtest.CertPEM(leaf),
		"example.com_apache/1_root_bundle.crt":     cryptoxtest.CertPEM(ca.Intermediate),
		"example.com_apache/3_example.com.key":     key,
		"example.com_iis/example.com.pfx":          "\x30\x82binary",
		"example.com_other/example.com.csr":        "-----BEGIN CERTIFICATE REQUEST-----",
	})
}

func openFetcher(t *testing.T, api *fakeAPI) *fetcher {
	t.Helper()
	m := &Module{open: func(id, key, endpoint string) (sslAPI, error) {
		if id != "AKID-id" || key != "s3cret" || endpoint != endpointChina {
			t.Errorf("opened %s %s %s", id, key, endpoint)
		}
		return api, nil
	}}
	p, err := m.fetchFactory(t.Context(), providers.Instance{Config: map[string]string{}, Secrets: map[string]string{"secret_id": "AKID-id", "secret_key": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*fetcher)
}

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
	api := &fakeAPI{
		listed: []issuedCert{
			{ID: "locked", Names: []string{"example.com", "www.example.com"}, End: "2099-01-01 00:00:00"},
			{ID: "one", Names: []string{"example.com"}, End: "2098-01-01 00:00:00", Download: true},
			{ID: "older", Names: []string{"example.com", "www.example.com"}, End: "2026-01-01 00:00:00", Download: true},
			{ID: "newest", Names: []string{"example.com", "www.example.com"}, End: "2027-01-01 00:00:00", Download: true},
		},
		archives: map[string][]byte{"newest": consoleArchive(t, ca, leaf, cryptoxtest.KeyPEM(t, key))},
	}
	s := openFetcher(t, api)

	// The newest certificate for both domains that may be downloaded,
	// with its chain, out of the folders of the archive.
	got, keys, err := collect(t, s, "", time.Time{}, "example.com", "www.example.com")
	if err != nil || len(got.Chain) != 2 || !got.Chain[0].Equal(leaf) || !got.Chain[1].Equal(ca.Intermediate) || !key.PublicKey.Equal(got.Key.Public()) {
		t.Fatalf("collect: %v %v", got, err)
	}
	if !slices.Equal(keys, []i18n.Key{i18n.TencentcloudLogFetchLook, i18n.TencentcloudLogFetchFound}) {
		t.Errorf("log %v", keys)
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
		if !errors.As(err, &w) || w.After != recheck || !slices.Contains(keys, i18n.TencentcloudLogFetchSame) {
			t.Errorf("%s: %v, log %v", name, err, keys)
		}
	}

	// All it can take in, to pick from, ending in Beijing time.
	held, err := s.Held(t.Context())
	if err != nil || len(held) != 3 || held[0].Domains[0] != "example.com" || !held[2].NotAfter.Equal(time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("held %+v: %v", held, err)
	}

	// Without a key, as for a CSR of one's own; with a key that has a
	// password; with nothing that fits.
	api.archives["newest"] = archive(t, map[string]string{"example.com_nginx/example.com_bundle.crt": cryptoxtest.CertPEM(leaf, ca.Intermediate)})
	if _, _, err := collect(t, s, "", time.Time{}, "example.com", "www.example.com"); problemKey(err) != i18n.TencentcloudErrorFetchNoKey || !retry.IsPermanent(err) {
		t.Errorf("no key: %v", err)
	}
	api.archives["newest"] = archive(t, map[string]string{
		"a/example.com_bundle.crt": cryptoxtest.CertPEM(leaf),
		"a/example.com.key":        "-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIB\n-----END ENCRYPTED PRIVATE KEY-----\n",
	})
	if _, _, err := collect(t, s, "", time.Time{}, "example.com", "www.example.com"); problemKey(err) != i18n.TencentcloudErrorFetchBad || !retry.IsPermanent(err) {
		t.Errorf("encrypted key: %v", err)
	}
	api.archives["newest"] = []byte("not a zip")
	if _, _, err := collect(t, s, "", time.Time{}, "example.com", "www.example.com"); problemKey(err) != i18n.TencentcloudErrorFetchBad {
		t.Errorf("not an archive: %v", err)
	}
	if _, _, err := collect(t, s, "", time.Time{}, "other.example.com"); problemKey(err) != i18n.TencentcloudErrorFetchNone || retry.IsPermanent(err) {
		t.Errorf("none: %v", err)
	}
}

func TestBundleOfBounds(t *testing.T) {
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().AddDate(0, 3, 0), "example.com")
	big := archive(t, map[string]string{
		"a/example.com.key": cryptoxtest.KeyPEM(t, key),
		"a/example.com.crt": cryptoxtest.CertPEM(leaf) + strings.Repeat("x", archiveSize+1),
	})
	if _, err := bundleOf(big, []string{"example.com"}); err == nil {
		t.Error("read a file past the bound")
	}
	// A key of another certificate fits none.
	other := archive(t, map[string]string{"a/x.crt": cryptoxtest.CertPEM(leaf), "a/x.key": cryptoxtest.KeyPEM(t, cryptoxtest.NewKey(t))})
	if _, err := bundleOf(other, []string{"example.com"}); !errors.Is(err, cryptox.ErrKeyMismatch) {
		t.Errorf("another key: %v", err)
	}
}

func TestFetchTest(t *testing.T) {
	msg, err := openFetcher(t, &fakeAPI{certs: map[string]string{"a": "A", "b": "B"}}).Test(t.Context())
	if err != nil || msg.Key != i18n.TencentcloudTestFetchOk || msg.Params["count"] != 2 {
		t.Errorf("test: %+v %v", msg, err)
	}
}

func TestSDKFetch(t *testing.T) {
	var got []map[string]any
	zipped := archive(t, map[string]string{"a/b.key": "KEY"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var params map[string]any
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &params); err != nil {
			t.Error(err)
		}
		got = append(got, params)
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("X-TC-Action") {
		case "DescribeCertificates":
			_, _ = io.WriteString(w, `{"Response":{"TotalCount":1,"Certificates":[{"CertificateId":"c1","Domain":"example.com","SubjectAltName":["example.com","www.example.com"],"CertEndTime":"2027-01-01 07:59:59","AllowDownload":true},{"CertificateId":"c2","Domain":"example.com","CertEndTime":"2026-01-01 07:59:59"}],"RequestId":"r"}}`)
		case "DownloadCertificate":
			_, _ = io.WriteString(w, `{"Response":{"Content":"`+base64.StdEncoding.EncodeToString(zipped)+`","ContentType":"application/zip","RequestId":"r"}}`)
		}
	}))
	defer srv.Close()
	api, err := dial("AKID-id", "s3cret", strings.TrimPrefix(srv.URL, "http://"), "HTTP")
	if err != nil {
		t.Fatal(err)
	}
	list, err := api.issued(t.Context(), "example.com")
	if err != nil || len(list) != 2 || list[0].ID != "c1" || !slices.Equal(list[0].Names, []string{"example.com", "example.com", "www.example.com"}) ||
		list[0].End != "2027-01-01 07:59:59" || !list[0].Download || !list[1].Download {
		t.Errorf("issued %+v %v", list, err)
	}
	status, _ := got[0]["CertificateStatus"].([]any)
	if got[0]["SearchKey"] != "example.com" || got[0]["CertificateType"] != "SVR" || got[0]["FilterSource"] != "buy" || len(status) != 1 || status[0] != float64(statusIssued) {
		t.Errorf("list sent %v", got[0])
	}
	if data, err := api.download(t.Context(), "c1"); err != nil || !bytes.Equal(data, zipped) || got[1]["CertificateId"] != "c1" {
		t.Errorf("download %v, sent %v", err, got[1])
	}
}

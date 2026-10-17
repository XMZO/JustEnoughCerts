package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
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

// received is a request a test server got.
type received struct {
	header http.Header
	body   []byte
}

func server(t *testing.T, tls bool, answer func(w http.ResponseWriter)) (*httptest.Server, *[]received) {
	t.Helper()
	var got []received
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, received{header: r.Header.Clone(), body: body})
		answer(w)
	})
	var srv *httptest.Server
	if tls {
		srv = httptest.NewTLSServer(h)
	} else {
		srv = httptest.NewServer(h)
	}
	t.Cleanup(srv.Close)
	return srv, &got
}

func instance(config map[string]string, secrets map[string]string) providers.Instance {
	return providers.Instance{Config: config, Secrets: secrets}
}

var note = contracts.Note{Event: contracts.CertificateIssueFailed, Title: "Could not issue example.com", Text: "line", Time: time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)}

func TestNotify(t *testing.T) {
	srv, got := server(t, false, func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) })
	p, err := newNotifier(t.Context(), instance(
		map[string]string{"url": srv.URL + "/hook?key=abc", "allow_private": "true"},
		map[string]string{"secret": "shh", "headers": "Authorization: Bearer t0k\n\nX-Team: ops\n"}))
	if err != nil {
		t.Fatal(err)
	}
	n := p.(*notifier)
	if err := n.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("%d requests", len(*got))
	}
	r := (*got)[0]
	var sent contracts.Note
	if err := json.Unmarshal(r.body, &sent); err != nil || sent.Title != note.Title || sent.Event != note.Event {
		t.Errorf("body %s: %v", r.body, err)
	}
	mac := hmac.New(sha256.New, []byte("shh"))
	mac.Write(r.body)
	if r.header.Get("X-JEC-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) || r.header.Get("X-JEC-Event") != note.Event ||
		r.header.Get("Authorization") != "Bearer t0k" || r.header.Get("X-Team") != "ops" || r.header.Get("Content-Type") != "application/json" {
		t.Errorf("headers %v", r.header)
	}
	if n.e.shown() != srv.URL+"/hook" {
		t.Errorf("shown as %s", n.e.shown())
	}
	if err := n.Close(); err != nil {
		t.Error(err)
	}
}

func TestPrivateAddresses(t *testing.T) {
	srv, got := server(t, false, func(http.ResponseWriter) {})
	p, err := newNotifier(t.Context(), instance(map[string]string{"url": srv.URL}, nil))
	if err != nil {
		t.Fatal(err)
	}
	err = p.(*notifier).Notify(t.Context(), note)
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.WebhookErrorPrivate || !retry.IsPermanent(err) || len(*got) != 0 {
		t.Errorf("a loopback address: %v", err)
	}
}

func TestAnswers(t *testing.T) {
	for _, c := range []struct {
		status    int
		header    string
		key       i18n.Key
		permanent bool
		delay     time.Duration
	}{
		{http.StatusServiceUnavailable, "30", i18n.WebhookErrorStatus, false, 30 * time.Second},
		{http.StatusTooManyRequests, "", i18n.WebhookErrorStatus, false, 0},
		{http.StatusNotFound, "", i18n.WebhookErrorStatus, true, 0},
		{http.StatusFound, "", i18n.WebhookErrorRedirect, true, 0},
	} {
		srv, got := server(t, false, func(w http.ResponseWriter) {
			if c.header != "" {
				w.Header().Set("Retry-After", c.header)
			}
			w.Header().Set("Location", "https://elsewhere.example.com/")
			w.WriteHeader(c.status)
		})
		p, err := newNotifier(t.Context(), instance(map[string]string{"url": srv.URL, "allow_private": "true"}, nil))
		if err != nil {
			t.Fatal(err)
		}
		err = p.(*notifier).Notify(t.Context(), note)
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != c.key || retry.IsPermanent(err) != c.permanent || len(*got) != 1 {
			t.Errorf("%d: %v (%d requests)", c.status, err, len(*got))
		}
		if d, ok := retry.Delay(err); c.delay != 0 && (!ok || d != c.delay) {
			t.Errorf("%d: delay %v %v", c.status, d, ok)
		}
	}

	// Nobody listening.
	srv, _ := server(t, false, func(http.ResponseWriter) {})
	url := srv.URL
	srv.Close()
	p, err := newNotifier(t.Context(), instance(map[string]string{"url": url, "allow_private": "true"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	err = p.(*notifier).Notify(t.Context(), note)
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.WebhookErrorNetwork || retry.IsPermanent(err) {
		t.Errorf("nobody listening: %v", err)
	}
}

func TestSettings(t *testing.T) {
	var pr *contracts.Problem
	_, err := newNotifier(t.Context(), instance(map[string]string{"url": "https://example.com"}, map[string]string{"headers": "Authorization Bearer s3cret"}))
	if !errors.As(err, &pr) || pr.Key != i18n.WebhookErrorHeaders || strings.Contains(err.Error(), "s3cret") || !retry.IsPermanent(err) {
		t.Errorf("a bad header: %v", err)
	}
	if _, err := newNotifier(t.Context(), instance(map[string]string{"url": "ftp://example.com"}, nil)); !errors.As(err, &pr) || pr.Key != i18n.WebhookErrorUrl {
		t.Errorf("an ftp URL: %v", err)
	}
	if _, err := newDeployer(t.Context(), instance(map[string]string{"url": "http://example.com/deploy"}, nil)); !errors.As(err, &pr) || pr.Key != i18n.WebhookErrorHttps {
		t.Errorf("a deployment over http: %v", err)
	}
}

func TestDeploy(t *testing.T) {
	answer := `{"ref":"slot-2","removed":["slot-1"]}`
	srv, got := server(t, true, func(w http.ResponseWriter) { _, _ = io.WriteString(w, answer) })
	p, err := newDeployer(t.Context(), instance(map[string]string{"url": srv.URL + "/install", "allow_private": "true"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	d := p.(*deployer)
	// Trust the test server's certificate.
	d.e.client.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig

	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com")
	var logged []i18n.Key
	req := contracts.DeployRequest{CertificateID: 3, Domains: []string{"example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate},
		Key: key, Previous: []string{"slot-1"}, Log: func(l contracts.LogLine) { logged = append(logged, l.Key) }}
	res, err := d.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "slot-2" || !slices.Equal(res.Removed, []string{"slot-1"}) || !slices.Equal(logged, []i18n.Key{i18n.WebhookLogPost, i18n.WebhookLogRef}) {
		t.Errorf("result %+v, log %v", res, logged)
	}
	var sent Deployment
	if err := json.Unmarshal((*got)[0].body, &sent); err != nil {
		t.Fatal(err)
	}
	b, err := cryptox.ParseBundle([]byte(sent.Fullchain), []byte(sent.PrivKey))
	if err != nil || !b.Leaf.Equal(leaf) || sent.Event != DeployEvent || sent.Certificate != 3 || !slices.Equal(sent.Previous, []string{"slot-1"}) ||
		sent.Serial != leaf.SerialNumber.Text(16) || len(sent.Fingerprint) != 64 || (*got)[0].header.Get("X-JEC-Event") != DeployEvent {
		t.Errorf("sent %+v: %v", sent, err)
	}

	// An answer that is not JSON names nothing.
	answer = "ok"
	logged = nil
	if res, err := d.Deploy(t.Context(), req); err != nil || res.Ref != "" || !slices.Equal(logged, []i18n.Key{i18n.WebhookLogPost, i18n.WebhookLogAccepted}) {
		t.Errorf("a plain answer: %+v, %v, %v", res, err, logged)
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Duration{
		"": 0, "120": 2 * time.Minute, "soon": 0, "-5": 0,
		now.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second,
	} {
		if got := retryAfter(v, now); got != want {
			t.Errorf("retryAfter(%q) = %v", v, got)
		}
	}
}

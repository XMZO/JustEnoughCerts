package portainer

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

const apiKey = "ptr_s3cret"

// fakePortainer answers as Portainer's API does, keeping what is put, and
// what reply says for the rest.
type fakePortainer struct {
	t          *testing.T
	selfSigned bool
	put        map[string]any
	reply      func(r *http.Request) (int, string)
}

func (f *fakePortainer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-API-Key") != apiKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Invalid JWT token","details":"Unauthorized"}`)
		return
	}
	if f.reply != nil {
		if status, answer := f.reply(r); status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, answer)
			return
		}
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/ssl":
		b, _ := json.Marshal(settings{SelfSigned: f.selfSigned, HTTPEnabled: true})
		_, _ = w.Write(b)
	case r.Method == http.MethodPut && r.URL.Path == "/api/ssl":
		if err := json.NewDecoder(r.Body).Decode(&f.put); err != nil {
			f.t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not found"}`)
	}
}

func serve(t *testing.T, f *fakePortainer) *panel {
	t.Helper()
	f.t = t
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": srv.URL}, Secrets: map[string]string{"api_key": apiKey}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*panel)
}

func request(t *testing.T) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), "portainer.example")
	return contracts.DeployRequest{CertificateID: 1, Domains: []string{"portainer.example"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func TestDeployAndTest(t *testing.T) {
	f := &fakePortainer{selfSigned: true}
	p := serve(t, f)
	if msg, err := p.Test(t.Context()); err != nil || msg.Key != i18n.PortainerTestSelfSigned {
		t.Errorf("test %v %v", msg, err)
	}
	req := request(t)
	if _, err := p.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	key, _ := cryptox.EncodeKey(req.Key)
	if f.put["cert"] != string(cryptox.EncodeCertificates(req.Chain...)) || f.put["key"] != string(key) {
		t.Errorf("put %v", f.put)
	}
	f.selfSigned = false
	if msg, err := p.Test(t.Context()); err != nil || msg.Key != i18n.PortainerTestOk {
		t.Errorf("test %v %v", msg, err)
	}
}

func TestErrors(t *testing.T) {
	var p *contracts.Problem
	pl := serve(t, &fakePortainer{})
	pl.key = "wrong"
	if _, err := pl.Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.PortainerErrorKey || !retry.IsPermanent(err) {
		t.Errorf("a wrong key: %v", err)
	}
	for name, tt := range map[string]struct {
		status    int
		answer    string
		key       i18n.Key
		permanent bool
	}{
		"refused":     {http.StatusBadRequest, `{"message":"Invalid request payload","details":"tls: private key does not match public key"}`, i18n.PortainerErrorApi, true},
		"down":        {http.StatusInternalServerError, `{"message":"Unable to persist the certificate"}`, i18n.PortainerErrorApi, false},
		"not the API": {http.StatusNotFound, "<html>not found</html>", i18n.PortainerErrorStatus, true},
	} {
		pl := serve(t, &fakePortainer{reply: func(*http.Request) (int, string) { return tt.status, tt.answer }})
		_, err := pl.Deploy(t.Context(), request(t))
		if !errors.As(err, &p) || p.Key != tt.key || retry.IsPermanent(err) != tt.permanent {
			t.Errorf("%s: %v (permanent %v)", name, err, retry.IsPermanent(err))
		}
		// The details of a refusal come with its message.
		if name == "refused" && p.Params["message"] != "Invalid request payload: tls: private key does not match public key" {
			t.Errorf("message %v", p.Params)
		}
	}
}

func TestFactory(t *testing.T) {
	var p *contracts.Problem
	for _, tt := range []struct {
		config map[string]string
		key    i18n.Key
	}{
		{map[string]string{"url": "ftp://portainer.example"}, i18n.PortainerErrorUrl},
		{map[string]string{"url": "https://portainer.example", "fingerprint": "nope"}, i18n.PortainerErrorFingerprint},
	} {
		if _, err := factory(t.Context(), providers.Instance{Config: tt.config}); !errors.As(err, &p) || p.Key != tt.key {
			t.Errorf("%v: %v", tt.config, err)
		}
	}
	for url, key := range map[string]i18n.Key{"http://203.0.113.5:9000": i18n.PortainerErrorPlain, "https://127.0.0.1:1": i18n.PortainerErrorPrivate} {
		pl, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": url}, Secrets: map[string]string{"api_key": apiKey}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pl.(*panel).Test(t.Context()); !errors.As(err, &p) || p.Key != key {
			t.Errorf("%s: %v", url, err)
		}
	}
}

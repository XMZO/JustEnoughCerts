package acme

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func TestZeroSSLBinding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if r.Method != http.MethodPost || r.URL.Path != "/acme/eab-credentials-email" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if form.Get("email") == "bad@example.com" {
			_, _ = io.WriteString(w, `{"success":false,"error":{"code":2900,"type":"invalid_email","info":"no"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"success":true,"eab_kid":"kid-1","eab_hmac_key":"aG1hYw"}`)
	}))
	defer srv.Close()
	m := New()
	m.zeroSSL = srv.URL
	src := func(email string) *source {
		return &source{m: m, in: providers.Instance{Config: map[string]string{"ca": "zerossl", "email": email}}}
	}

	kid, hmac, err := src("admin@example.com").zeroSSLBinding(t.Context())
	if err != nil || kid != "kid-1" || hmac != "aG1hYw" {
		t.Errorf("binding = %q %q %v", kid, hmac, err)
	}
	var p *contracts.Problem
	if _, _, err := src("bad@example.com").zeroSSLBinding(t.Context()); !errors.As(err, &p) || p.Key != i18n.AcmeErrorZerossl ||
		p.Params["message"] != "invalid_email no" || !retry.IsPermanent(err) {
		t.Errorf("refused: %v", err)
	}
	if _, _, err := src("").zeroSSLBinding(t.Context()); !errors.As(err, &p) || p.Key != i18n.AcmeErrorZerosslEmail {
		t.Errorf("no email: %v", err)
	}
}

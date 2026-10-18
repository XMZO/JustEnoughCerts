package pushover

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func serve(t *testing.T, h http.HandlerFunc, device string) *user {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"device": device}, Secrets: map[string]string{"token": "app", "user": "me"}})
	if err != nil {
		t.Fatal(err)
	}
	u := p.(*user)
	u.client, u.endpoint = srv.Client(), srv.URL+"/1/messages.json"
	return u
}

func TestNotify(t *testing.T) {
	var got url.Values
	u := serve(t, func(rw http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		_, _ = io.WriteString(rw, `{"status": 1, "request": "abc"}`)
	}, "phone")
	n := contracts.Note{Event: contracts.CertificateExpiring, Title: "Expiring", Subject: "[JEC] Expiring", Text: strings.Repeat("é", 2000), Link: "https://jec.example/certificates/1"}
	if err := u.Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	if got.Get("token") != "app" || got.Get("user") != "me" || got.Get("device") != "phone" || got.Get("title") != "[JEC] Expiring" ||
		utf8.RuneCountInString(got.Get("message")) != maxMessage || got.Get("url") != n.Link || got.Get("priority") != "1" {
		t.Errorf("pushed %v", got)
	}
	n.Event = contracts.CertificateIssued
	if err := u.Notify(t.Context(), n); err != nil || got.Get("priority") != "" {
		t.Errorf("issued: priority %q %v", got.Get("priority"), err)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{400, `{"user": "invalid", "errors": ["user identifier is invalid"], "status": 0}`, i18n.PushoverErrorKeys, true},
		{400, `{"token": "invalid", "errors": ["application token is invalid"], "status": 0}`, i18n.PushoverErrorKeys, true},
		{400, `{"errors": ["message cannot be blank"], "status": 0}`, i18n.PushoverErrorApi, true},
		{429, `{"errors": ["application over quota"], "status": 0}`, i18n.PushoverErrorApi, false},
		{503, `<html>down</html>`, i18n.PushoverErrorStatus, false},
	} {
		u := serve(t, func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(c.status)
			_, _ = io.WriteString(rw, c.body)
		}, "")
		err := u.Notify(t.Context(), contracts.Note{Title: "t", Text: "x"})
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
}

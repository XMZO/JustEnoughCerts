package onebot

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func open(t *testing.T, config map[string]string) *bot {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"token": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*bot)
}

func TestNotify(t *testing.T) {
	var path, auth string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(rw, `{"status": "ok", "retcode": 0, "data": {"message_id": 1}}`)
	}))
	defer srv.Close()
	n := contracts.Note{Title: "Issued", Subject: "[JEC] Issued", Text: "example.com [CQ:face,id=1]", Link: "https://jec.example/certificates/1"}

	b := open(t, map[string]string{"server": srv.URL + "/", "target": "123456", "allow_private": "true"})
	if err := b.Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	if path != "/send_group_msg" || auth != "Bearer secret" || got["group_id"] != float64(123456) || got["auto_escape"] != true ||
		got["message"] != "[JEC] Issued\nexample.com [CQ:face,id=1]\nhttps://jec.example/certificates/1" {
		t.Errorf("posted %s %s %v", path, auth, got)
	}
	b = open(t, map[string]string{"server": srv.URL, "to": "private", "target": "42", "allow_private": "true"})
	if err := b.Notify(t.Context(), n); err != nil || path != "/send_private_msg" || got["user_id"] != float64(42) {
		t.Errorf("a person: %s %v %v", path, got, err)
	}

	// Without the switch, a bot on this host is out of reach.
	b = open(t, map[string]string{"server": srv.URL, "target": "1"})
	var p *contracts.Problem
	if err := b.Notify(t.Context(), n); !errors.As(err, &p) || p.Key != i18n.OnebotErrorPrivate || !retry.IsPermanent(err) {
		t.Errorf("a private address: %v", err)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{401, ``, i18n.OnebotErrorToken, true},
		{403, ``, i18n.OnebotErrorToken, true},
		{200, `{"status": "failed", "retcode": 1200, "wording": "not in the group"}`, i18n.OnebotErrorApi, true},
		{502, ``, i18n.OnebotErrorApi, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(c.status)
			_, _ = io.WriteString(rw, c.body)
		}))
		b := open(t, map[string]string{"server": srv.URL, "target": "1", "allow_private": "true"})
		err := b.Notify(t.Context(), contracts.Note{Title: "t"})
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
		srv.Close()
	}
	for _, cfg := range []map[string]string{{"server": "ftp://x", "target": "1"}, {"server": "http://x", "target": "abc"}, {"server": "http://x", "target": "-1"}} {
		if _, err := factory(t.Context(), providers.Instance{Config: cfg}); err == nil || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", cfg, err)
		}
	}
}

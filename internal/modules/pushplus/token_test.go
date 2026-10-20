package pushplus

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

const token = "0123456789abcdef0123456789abcdef"

func serve(t *testing.T, config map[string]string, h http.HandlerFunc) *pusher {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := endpoint
	endpoint = srv.URL + "/send"
	t.Cleanup(func() { endpoint = old })
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"token": token}})
	if err != nil {
		t.Fatal(err)
	}
	pp := p.(*pusher)
	pp.client = srv.Client()
	return pp
}

func TestNotify(t *testing.T) {
	var got map[string]string
	p := serve(t, map[string]string{"topic": "ops"}, func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(rw, `{"code": 200, "msg": "ok", "data": "x"}`)
	})
	n := contracts.Note{Event: contracts.CertificateIssued, Title: "Issued", Subject: "[JEC] Issued", Text: "a <b>\nb", Link: "https://jec.example/c?a=1&b=2"}
	if err := p.Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	if got["token"] != token || got["title"] != "[JEC] Issued" || got["topic"] != "ops" || got["channel"] != "wechat" || got["template"] != "html" ||
		got["content"] != `a &lt;b&gt;<br>b<br><br><a href="https://jec.example/c?a=1&amp;b=2">https://jec.example/c?a=1&amp;b=2</a>` {
		t.Errorf("posted %v", got)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{200, `{"code": 903, "msg": "invalid token"}`, i18n.PushplusErrorToken, true},
		{200, `{"code": 900, "msg": "restricted"}`, i18n.PushplusErrorApi, true},
		{200, `{"code": 999, "msg": "server error"}`, i18n.PushplusErrorApi, false},
		{502, `bad gateway`, i18n.PushplusErrorApi, false},
	} {
		p := serve(t, nil, func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(c.status)
			_, _ = io.WriteString(rw, c.body)
		})
		err := p.Notify(t.Context(), contracts.Note{Title: "t"})
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
	if _, err := factory(t.Context(), providers.Instance{Secrets: map[string]string{"token": "not a token"}}); err == nil || !retry.IsPermanent(err) {
		t.Errorf("a bad token: %v", err)
	}
}

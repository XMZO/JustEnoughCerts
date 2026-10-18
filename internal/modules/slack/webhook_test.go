package slack

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func serve(t *testing.T, h http.HandlerFunc) *webhook {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"allow_private": "true"}, Secrets: map[string]string{"url": srv.URL + "/services/T/B/x"}})
	if err != nil {
		t.Fatal(err)
	}
	w := p.(*webhook)
	w.client = srv.Client()
	return w
}

func TestNotify(t *testing.T) {
	var got map[string]any
	w := serve(t, func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(rw, "ok")
	})
	n := contracts.Note{Title: "Issued <a.example>", Subject: "[JEC] Issued <a.example>", Text: "A & B", Link: "https://jec.example/certificates/1"}
	if err := w.Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	if got["text"] != "[JEC] Issued &lt;a.example&gt;\n\nA &amp; B\nhttps://jec.example/certificates/1" || got["username"] != "JEC" {
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
		{403, "invalid_token", i18n.SlackErrorWebhook, true},
		{410, "channel_is_archived", i18n.SlackErrorWebhook, true},
		{400, "invalid_payload", i18n.SlackErrorApi, true},
		{429, "rate_limited", i18n.SlackErrorApi, false},
		{500, "<html>oops</html>", i18n.SlackErrorApi, false},
	} {
		w := serve(t, func(rw http.ResponseWriter, _ *http.Request) {
			if c.status == 429 {
				rw.Header().Set("Retry-After", "30")
			}
			rw.WriteHeader(c.status)
			_, _ = io.WriteString(rw, c.body)
		})
		err := w.Notify(t.Context(), contracts.Note{Title: "t"})
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
		if c.status == 429 {
			if d, ok := retry.Delay(err); !ok || d != 30*time.Second {
				t.Errorf("429: delay %v %v", d, ok)
			}
		}
		if c.status == 500 && strings.Contains(pr.Params["reason"].(string), "<") {
			t.Errorf("500: a page as the reason: %v", pr.Params)
		}
	}
	if _, err := factory(t.Context(), providers.Instance{Secrets: map[string]string{"url": "http://hooks.slack.com/services/x"}}); err == nil {
		t.Error("plain HTTP")
	}
	w, err := factory(t.Context(), providers.Instance{Secrets: map[string]string{"url": "https://127.0.0.1:9/hooks/x"}})
	if err != nil {
		t.Fatal(err)
	}
	err = w.(*webhook).Notify(t.Context(), contracts.Note{Title: "t"})
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.SlackErrorPrivate || !retry.IsPermanent(err) {
		t.Errorf("private: %v", err)
	}
}

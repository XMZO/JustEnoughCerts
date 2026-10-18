package discord

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func serve(t *testing.T, h http.HandlerFunc) *webhook {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := factory(t.Context(), providers.Instance{Secrets: map[string]string{"url": "https://discord.com/api/webhooks/1/token"}})
	if err != nil {
		t.Fatal(err)
	}
	w := p.(*webhook)
	if !strings.HasSuffix(w.url, "?wait=true") {
		t.Errorf("url %s", w.url)
	}
	w.client, w.url = srv.Client(), srv.URL+"/api/webhooks/1/token?wait=true"
	return w
}

func TestNotify(t *testing.T) {
	var got map[string]any
	w := serve(t, func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		rw.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(rw, `{"id": "1"}`)
	})
	n := contracts.Note{Event: contracts.CertificateIssueFailed, Title: "Issue failed", Subject: "[JEC] Issue failed",
		Text: strings.Repeat("x", 5000), Link: "https://jec.example/certificates/1", Time: time.Unix(1760000000, 0)}
	if err := w.Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	embed := got["embeds"].([]any)[0].(map[string]any)
	if embed["title"] != "[JEC] Issue failed" || embed["url"] != n.Link || embed["color"] != float64(colorDanger) ||
		utf8.RuneCountInString(embed["description"].(string)) != maxDescription || got["username"] != "JEC" {
		t.Errorf("posted %v", got)
	}
	if color(contracts.CertificateDeployed) != colorOK || color(contracts.CertificateExpiring) != colorWarn || color(contracts.NoteTest) != colorOther {
		t.Error("colors")
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
		after     bool
	}{
		{404, `{"message": "Unknown Webhook", "code": 10015}`, i18n.DiscordErrorWebhook, true, false},
		{400, `{"message": "Invalid Form Body"}`, i18n.DiscordErrorApi, true, false},
		{429, `{"message": "You are being rate limited.", "retry_after": 1.5}`, i18n.DiscordErrorApi, false, true},
		{502, `bad gateway`, i18n.DiscordErrorApi, false, false},
	} {
		w := serve(t, func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(c.status)
			_, _ = io.WriteString(rw, c.body)
		})
		err := w.Notify(t.Context(), contracts.Note{Title: "t", Text: "x"})
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d: %v", c.status, err)
		}
		if d, ok := retry.Delay(err); c.after && (!ok || d != 1500*time.Millisecond) {
			t.Errorf("%d: delay %v %v", c.status, d, ok)
		}
	}
	for _, u := range []string{"http://discord.com/api/webhooks/1/x", "https://example.com/api/webhooks/1/x", "https://discord.com/channels/1"} {
		if _, err := factory(t.Context(), providers.Instance{Secrets: map[string]string{"url": u}}); err == nil || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", u, err)
		}
	}
}

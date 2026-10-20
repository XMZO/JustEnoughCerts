package googlechat

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

const hook = "https://chat.googleapis.com/v1/spaces/AAAA/messages?key=k&token=t"

func serve(t *testing.T, h http.HandlerFunc) *webhook {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := factory(t.Context(), providers.Instance{Secrets: map[string]string{"url": hook}})
	if err != nil {
		t.Fatal(err)
	}
	w := p.(*webhook)
	w.client, w.url = srv.Client(), srv.URL+"/v1/spaces/AAAA/messages"
	return w
}

func TestNotify(t *testing.T) {
	var got map[string]string
	w := serve(t, func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(rw, `{"name": "spaces/AAAA/messages/1"}`)
	})
	n := contracts.Note{Event: contracts.CertificateIssued, Title: "Issued", Subject: "[JEC] *Issued*", Text: "example.com", Link: "https://jec.example/certificates/1"}
	if err := w.Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	if got["text"] != "*[JEC] Issued*\nexample.com\nhttps://jec.example/certificates/1" {
		t.Errorf("posted %q", got["text"])
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		permanent bool
	}{
		{400, `{"error": {"code": 400, "message": "Invalid JSON payload", "status": "INVALID_ARGUMENT"}}`, true},
		{403, `{"error": {"code": 403, "message": "The caller does not have permission", "status": "PERMISSION_DENIED"}}`, true},
		{429, `{"error": {"code": 429, "status": "RESOURCE_EXHAUSTED"}}`, false},
		{503, ``, false},
	} {
		w := serve(t, func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(c.status)
			_, _ = io.WriteString(rw, c.body)
		})
		err := w.Notify(t.Context(), contracts.Note{Title: "t"})
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.GooglechatErrorApi || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d: %v", c.status, err)
		}
	}
	for _, u := range []string{"http://chat.googleapis.com/v1/spaces/A/messages", "https://example.com/v1/spaces/A/messages", "https://chat.googleapis.com/v1/spaces/A"} {
		if _, err := factory(t.Context(), providers.Instance{Secrets: map[string]string{"url": u}}); err == nil || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", u, err)
		}
	}
}

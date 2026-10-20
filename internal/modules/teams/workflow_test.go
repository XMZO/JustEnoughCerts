package teams

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

const hook = "https://prod-01.westus.logic.azure.com:443/workflows/abc/triggers/manual/paths/invoke?api-version=2016-06-01&sig=secret"

func serve(t *testing.T, h http.HandlerFunc) *workflow {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := factory(t.Context(), providers.Instance{Secrets: map[string]string{"url": hook}})
	if err != nil {
		t.Fatal(err)
	}
	w := p.(*workflow)
	if w.url != hook {
		t.Errorf("url %s", w.url)
	}
	w.client, w.url = srv.Client(), srv.URL+"/workflows/abc"
	return w
}

func TestNotify(t *testing.T) {
	var got map[string]any
	w := serve(t, func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		rw.WriteHeader(http.StatusAccepted)
	})
	n := contracts.Note{Event: contracts.CertificateIssueFailed, Title: "Issue failed", Subject: "[JEC] Issue failed [x]",
		Text: "example.com\n\nThe CA said no", Link: "https://jec.example/certificates/1", Time: time.Unix(1760000000, 0)}
	if err := w.Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	content := got["attachments"].([]any)[0].(map[string]any)["content"].(map[string]any)
	body := content["body"].([]any)
	title := body[0].(map[string]any)
	if got["type"] != "message" || content["type"] != "AdaptiveCard" || len(body) != 3 ||
		title["text"] != "[(JEC) Issue failed (x)](https://jec.example/certificates/1)" || title["color"] != "attention" ||
		body[2].(map[string]any)["text"] != "The CA said no" {
		t.Errorf("posted %v", got)
	}
	if color(contracts.CertificateDeployed) != "good" || color(contracts.CertificateExpiring) != "warning" || color(contracts.NoteTest) != "default" {
		t.Error("colors")
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		permanent bool
		message   string
	}{
		{400, `{"error":{"code":"WorkflowTriggerIsNotEnabled","message":"The workflow is disabled."}}`, true, "The workflow is disabled."},
		{404, `not found`, true, "not found"},
		{429, ``, false, "Too Many Requests"},
		{502, `bad gateway`, false, "bad gateway"},
	} {
		w := serve(t, func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(c.status)
			_, _ = io.WriteString(rw, c.body)
		})
		err := w.Notify(t.Context(), contracts.Note{Title: "t", Text: "x"})
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.TeamsErrorApi || retry.IsPermanent(err) != c.permanent || p.Params["message"] != c.message {
			t.Errorf("%d: %v", c.status, err)
		}
	}
	for _, u := range []string{"http://prod-01.westus.logic.azure.com/workflows/x", "https://example.com/workflows/x", "https://outlook.office.com/webhook/x", "not a url"} {
		if _, err := factory(t.Context(), providers.Instance{Secrets: map[string]string{"url": u}}); err == nil || !retry.IsPermanent(err) || !strings.Contains(err.Error(), "") {
			t.Errorf("%s: %v", u, err)
		}
	}
	if _, err := factory(t.Context(), providers.Instance{Secrets: map[string]string{"url": "https://default0a1b.2c.environment.api.powerplatform.com:443/powerautomate/automations/direct/workflows/x/triggers/manual/paths/invoke?sig=y"}}); err != nil {
		t.Errorf("a Power Platform URL: %v", err)
	}
}

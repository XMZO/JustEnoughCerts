package gotify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// TestLive sends to a real Gotify server and reads the message back. CI
// does not run it: JEC_TEST_GOTIFY is the server, such as
// http://127.0.0.1:8091, JEC_TEST_GOTIFY_TOKEN the token of an
// application on it, and JEC_TEST_GOTIFY_USER a user:password that reads
// the messages.
func TestLive(t *testing.T) {
	server, token, user := os.Getenv("JEC_TEST_GOTIFY"), os.Getenv("JEC_TEST_GOTIFY_TOKEN"), os.Getenv("JEC_TEST_GOTIFY_USER")
	if server == "" || token == "" {
		t.Skip("JEC_TEST_GOTIFY and JEC_TEST_GOTIFY_TOKEN are not set")
	}
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"server": server, "allow_private": "true"}, Secrets: map[string]string{"token": token}})
	if err != nil {
		t.Fatal(err)
	}
	// In Chinese, escaped for the i18n gate: "deployment failed".
	n := contracts.Note{Event: contracts.CertificateDeployFailed, Title: "\u90e8\u7f72\u5931\u8d25", Subject: "\u3010JEC\u3011\u90e8\u7f72\u5931\u8d25", Text: "example.com \u90e8\u7f72\u5230 ESA \u5931\u8d25\u4e86\u3002",
		Link: "https://jec.example/certificates/1"}
	if err := p.(*app).Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server+"/message?limit=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	name, password, _ := strings.Cut(user, ":")
	req.SetBasicAuth(name, password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page struct {
		Messages []struct {
			Title    string         `json:"title"`
			Message  string         `json:"message"`
			Priority int            `json:"priority"`
			Extras   map[string]any `json:"extras"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	// The title alone: in Gotify the application already says JEC.
	if len(page.Messages) != 1 || page.Messages[0].Title != n.Title || page.Messages[0].Message != n.Text || page.Messages[0].Priority != priorityHigh ||
		!strings.Contains(fmt.Sprint(page.Messages[0].Extras["client::notification"]), n.Link) {
		t.Errorf("Gotify has %+v", page.Messages)
	}
}

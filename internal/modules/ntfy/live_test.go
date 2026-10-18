package ntfy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// TestLive publishes to a real ntfy server and reads the message back. CI
// does not run it: JEC_TEST_NTFY is the server, such as
// http://127.0.0.1:8090.
func TestLive(t *testing.T) {
	server := os.Getenv("JEC_TEST_NTFY")
	if server == "" {
		t.Skip("JEC_TEST_NTFY is not set")
	}
	name := fmt.Sprintf("jec-live-%d", time.Now().UnixNano())
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"server": server, "allow_private": "true"}, Secrets: map[string]string{"topic": name}})
	if err != nil {
		t.Fatal(err)
	}
	n := contracts.Note{Event: contracts.CertificateIssueFailed, Title: "签发失败", Subject: "【JEC】签发失败", Text: "example.com 的签发失败了。",
		Link: "https://jec.example/certificates/1"}
	if err := p.(*topic).Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(server + "/" + name + "/json?poll=1") //nolint:noctx // a test against a local server
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got []map[string]any
	for s := bufio.NewScanner(resp.Body); s.Scan(); {
		var m map[string]any
		if json.Unmarshal(s.Bytes(), &m) == nil && m["event"] == "message" {
			got = append(got, m)
		}
	}
	if len(got) != 1 || got[0]["title"] != n.Subject || got[0]["message"] != n.Text || got[0]["priority"] != float64(4) ||
		got[0]["click"] != n.Link || fmt.Sprint(got[0]["tags"]) != "[warning]" {
		t.Errorf("ntfy has %v", got)
	}
}

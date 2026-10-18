package email

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// TestLive sends a note in Chinese through a real SMTP server and reads it
// back from Mailpit's API. CI does not run it: JEC_TEST_SMTP is the
// server, as host:port without TLS, such as 127.0.0.1:1025, and
// JEC_TEST_MAILPIT the address of Mailpit's API, such as
// http://127.0.0.1:8025.
func TestLive(t *testing.T) {
	addr, api := os.Getenv("JEC_TEST_SMTP"), os.Getenv("JEC_TEST_MAILPIT")
	if addr == "" || api == "" {
		t.Skip("JEC_TEST_SMTP and JEC_TEST_MAILPIT are not set")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{
		"host": host, "port": port, "security": noTLS, "from": "JEC <jec@example.com>", "to": "admin@example.com, ops@example.com",
	}})
	if err != nil {
		t.Fatal(err)
	}
	subject := "【JEC】证书 example.com 已签发 " + time.Now().Format("150405.000")
	n := contracts.Note{Event: contracts.CertificateIssued, Title: "证书 example.com 已签发", Subject: subject,
		Text: "新证书的有效期到 2027 年 1 月 16 日。\n下一行：部署到 3 个目标。", Link: "https://jec.example/certificates/1", Time: time.Now()}
	if err := p.(*mailer).Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}

	get := func(path string, out any) {
		t.Helper()
		resp, err := http.Get(api + path) //nolint:noctx // a test against a local server
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	type address struct{ Address string }
	var list struct {
		Messages []struct {
			ID      string
			Subject string
		}
	}
	get("/api/v1/messages", &list)
	i := slices.IndexFunc(list.Messages, func(m struct{ ID, Subject string }) bool { return m.Subject == subject })
	if i < 0 {
		t.Fatalf("no message %q among %+v", subject, list.Messages)
	}
	var msg struct {
		Subject string
		From    address
		To      []address
		Text    string
	}
	get("/api/v1/message/"+list.Messages[i].ID, &msg)
	if msg.From.Address != "jec@example.com" || len(msg.To) != 2 || !strings.Contains(msg.Text, "部署到 3 个目标") {
		t.Errorf("message %+v", msg)
	}
	var headers map[string][]string
	get("/api/v1/message/"+list.Messages[i].ID+"/headers", &headers)
	if fmtHeader := headers["X-Jec-Event"]; len(fmtHeader) == 0 || fmtHeader[0] != contracts.CertificateIssued {
		t.Errorf("headers %v", headers)
	}
}

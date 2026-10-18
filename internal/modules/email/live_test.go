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
	// In Chinese, as the notes of a zh-CN notifier are: "the certificate
	// example.com was issued", and a text of two lines. The escapes keep CJK
	// text out of the code, as the i18n gate has it.
	subject := "\u3010JEC\u3011\u8bc1\u4e66 example.com \u5df2\u7b7e\u53d1 " + time.Now().Format("150405.000")
	n := contracts.Note{Event: contracts.CertificateIssued, Title: "\u8bc1\u4e66 example.com \u5df2\u7b7e\u53d1", Subject: subject,
		Text: "\u65b0\u8bc1\u4e66\u7684\u6709\u6548\u671f\u5230 2027 \u5e74 1 \u6708 16 \u65e5\u3002\n\u4e0b\u4e00\u884c\uff1a\u90e8\u7f72\u5230 3 \u4e2a\u76ee\u6807\u3002", Link: "https://jec.example/certificates/1", Time: time.Now()}
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
	if msg.From.Address != "jec@example.com" || len(msg.To) != 2 || !strings.Contains(msg.Text, "\u90e8\u7f72\u5230 3 \u4e2a\u76ee\u6807") {
		t.Errorf("message %+v", msg)
	}
	var headers map[string][]string
	get("/api/v1/message/"+list.Messages[i].ID+"/headers", &headers)
	if fmtHeader := headers["X-Jec-Event"]; len(fmtHeader) == 0 || fmtHeader[0] != contracts.CertificateIssued {
		t.Errorf("headers %v", headers)
	}
}

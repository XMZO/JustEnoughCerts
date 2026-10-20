package matrix

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// TestLive sends to a room of a real homeserver and reads the message
// back. CI does not run it: JEC_TEST_MATRIX is the homeserver, such as
// http://127.0.0.1:6167, JEC_TEST_MATRIX_TOKEN the access token of an
// account in the room, and JEC_TEST_MATRIX_ROOM the room's alias.
func TestLive(t *testing.T) {
	server, token, alias := os.Getenv("JEC_TEST_MATRIX"), os.Getenv("JEC_TEST_MATRIX_TOKEN"), os.Getenv("JEC_TEST_MATRIX_ROOM")
	if server == "" || token == "" || alias == "" {
		t.Skip("JEC_TEST_MATRIX, JEC_TEST_MATRIX_TOKEN and JEC_TEST_MATRIX_ROOM are not set")
	}
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"server": server, "room": alias, "allow_private": "true"}, Secrets: map[string]string{"token": token}})
	if err != nil {
		t.Fatal(err)
	}
	r := p.(*room)
	// In Chinese, escaped for the i18n gate: "deployment failed".
	n := contracts.Note{Event: contracts.CertificateDeployFailed, Title: "\u90e8\u7f72\u5931\u8d25", Subject: "\u3010JEC\u3011\u90e8\u7f72\u5931\u8d25", Text: "example.com \u90e8\u7f72\u5230 ESA \u5931\u8d25\u4e86\u3002",
		Link: "https://jec.example/certificates/1"}
	if err := r.Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	var page struct {
		Chunk []struct {
			Type    string         `json:"type"`
			Content map[string]any `json:"content"`
		} `json:"chunk"`
	}
	if err := r.call(t.Context(), http.MethodGet, "/_matrix/client/v3/rooms/"+url.PathEscape(r.id)+"/messages?dir=b&limit=1", nil, &page); err != nil {
		t.Fatal(err)
	}
	want := content(n)
	if len(page.Chunk) != 1 || page.Chunk[0].Type != "m.room.message" || page.Chunk[0].Content["body"] != want["body"] ||
		page.Chunk[0].Content["formatted_body"] != want["formatted_body"] || page.Chunk[0].Content["msgtype"] != "m.notice" {
		got, _ := json.Marshal(page)
		t.Errorf("the room has %s", got)
	}
}

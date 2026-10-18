package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// chat is a body for a chat service that takes JSON of its own.
const chat = `{"msgtype": "text", "text": {"content": {{json .subject}}},
 "link": {{json .link}}, "certificate": {{with .data.certificateId}}{{.}}{{else}}0{{end}},
 "domain": {{with .data.domains}}{{json (index . 0)}}{{else}}null{{end}}, "at": "{{.time}}"}`

// chatBody is what chat makes.
type chatBody struct {
	Msgtype string `json:"msgtype"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
	Link        string  `json:"link"`
	Certificate int64   `json:"certificate"`
	Domain      *string `json:"domain"`
	At          string  `json:"at"`
}

func TestNotifyTemplate(t *testing.T) {
	srv, got := server(t, false, func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) })
	p, err := newNotifier(t.Context(), instance(
		map[string]string{"url": srv.URL, "allow_private": "true", "method": http.MethodPut, "body": chat},
		map[string]string{"secret": "shh"}))
	if err != nil {
		t.Fatal(err)
	}
	n := p.(*notifier)
	about := note
	about.Subject = `[JEC] <example.com> & "more"`
	about.Data = json.RawMessage(`{"certificateId": 12345678901, "domains": ["example.com", "www.example.com"]}`)
	if err := n.Notify(t.Context(), about); err != nil {
		t.Fatal(err)
	}
	// A test note has no data, and no link without JEC's URL.
	test := contracts.Note{Event: contracts.NoteTest, Title: "A test", Subject: "[JEC] A test", Time: note.Time}
	if err := n.Notify(t.Context(), test); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("%d requests", len(*got))
	}
	for i, want := range []struct {
		subject string
		cert    int64
		domain  string
	}{{about.Subject, 12345678901, "example.com"}, {test.Subject, 0, ""}} {
		r := (*got)[i]
		var b chatBody
		if err := json.Unmarshal(r.body, &b); err != nil {
			t.Fatalf("not JSON: %s: %v", r.body, err)
		}
		domain := ""
		if b.Domain != nil {
			domain = *b.Domain
		}
		if b.Msgtype != "text" || b.Text.Content != want.subject || b.Link != "" || b.Certificate != want.cert || domain != want.domain || b.At != "2026-10-17T08:00:00Z" {
			t.Errorf("body %s", r.body)
		}
		mac := hmac.New(sha256.New, []byte("shh"))
		mac.Write(r.body)
		if r.method != http.MethodPut || r.header.Get("Content-Type") != "application/json" || r.header.Get("X-JEC-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Errorf("%s with headers %v", r.method, r.header)
		}
	}
}

func TestNotifyForm(t *testing.T) {
	srv, got := server(t, false, func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) })
	p, err := newNotifier(t.Context(), instance(map[string]string{
		"url": srv.URL, "allow_private": "true",
		"body": "token=abc&title={{urlquery .subject}}&content={{urlquery .text}}", "content_type": "application/x-www-form-urlencoded",
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	about := note
	about.Subject, about.Text = "[JEC] a&b=c", "line 1\nline 2"
	if err := p.(*notifier).Notify(t.Context(), about); err != nil {
		t.Fatal(err)
	}
	r := (*got)[0]
	form, err := url.ParseQuery(string(r.body))
	if err != nil || form.Get("token") != "abc" || form.Get("title") != about.Subject || form.Get("content") != about.Text ||
		r.method != http.MethodPost || r.header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Errorf("%s %s with headers %v: %v", r.method, r.body, r.header, err)
	}
}

func TestTemplateErrors(t *testing.T) {
	var p *contracts.Problem
	if _, err := newNotifier(t.Context(), instance(map[string]string{"url": "https://example.com", "body": "{{.subject"}, nil)); !errors.As(err, &p) || p.Key != i18n.WebhookErrorTemplate || !retry.IsPermanent(err) {
		t.Errorf("a template that does not parse: %v", err)
	}
	srv, got := server(t, false, func(http.ResponseWriter) {})
	n, err := newNotifier(t.Context(), instance(map[string]string{"url": srv.URL, "allow_private": "true", "body": `{{template "nowhere"}}`}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := n.(*notifier).Notify(t.Context(), note); !errors.As(err, &p) || p.Key != i18n.WebhookErrorTemplate || !retry.IsPermanent(err) || len(*got) != 0 {
		t.Errorf("a template that fails: %v", err)
	}
}

func TestDeployTemplate(t *testing.T) {
	srv, got := server(t, true, func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) })
	p, err := newDeployer(t.Context(), instance(map[string]string{
		"url": srv.URL + "/api/certificate", "allow_private": "true", "method": http.MethodPut,
		"body": `{"domain": {{json (index .domains 0)}}, "cert": {{json .fullchain}}, "key": {{json .privkey}}, "expires": {{json .not_after}}}`,
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	d := p.(*deployer)
	d.e.client.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com", "www.example.com")
	req := contracts.DeployRequest{CertificateID: 3, Domains: []string{"example.com", "www.example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
	if _, err := d.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	r := (*got)[0]
	var sent struct{ Domain, Cert, Key, Expires string }
	if err := json.Unmarshal(r.body, &sent); err != nil {
		t.Fatalf("not JSON: %s: %v", r.body, err)
	}
	b, err := cryptox.ParseBundle([]byte(sent.Cert), []byte(sent.Key))
	if err != nil || !b.Leaf.Equal(leaf) || len(b.Chain) != 1 || sent.Domain != "example.com" || sent.Expires != leaf.NotAfter.UTC().Format(time.RFC3339) || r.method != http.MethodPut {
		t.Errorf("%s %+v: %v", r.method, sent, err)
	}
}

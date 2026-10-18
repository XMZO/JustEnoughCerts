package wecom

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

const key = "0123abcd-robot-key"

// serve starts a stand-in for WeCom and a module that reaches it.
func serve(t *testing.T, h http.HandlerFunc) *Module {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	m := New()
	m.client, m.base = srv.Client(), srv.URL+"/cgi-bin/webhook/send"
	return m
}

func open(t *testing.T, m *Module, config, secrets map[string]string) *robot {
	t.Helper()
	p, err := m.factory(t.Context(), providers.Instance{Config: config, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*robot)
}

var note = contracts.Note{Event: contracts.CertificateIssueFailed, Title: "example.com failed", Text: "line 1\nline 2"}

func TestNotify(t *testing.T) {
	var got struct {
		Type string `json:"msgtype"`
		Text struct {
			Content string `json:"content"`
		} `json:"text"`
	}
	m := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/cgi-bin/webhook/send" || r.URL.Query().Get("key") != key || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s", r.Method, r.URL)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	})

	r := open(t, m, map[string]string{contracts.NoteLocale: "en-US"}, map[string]string{"webhook": " " + m.base + "?key=" + key + " "})
	if err := r.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	if got.Type != "text" || got.Text.Content != "[JEC] example.com failed\n\nline 1\nline 2" {
		t.Errorf("sent %+v", got)
	}
	if err := r.Close(); err != nil {
		t.Error(err)
	}

	// WeCom takes 2048 bytes of text at most: the rest is cut, between
	// characters.
	long := contracts.Note{Title: "t", Text: strings.Repeat("ab"+string(rune(0x4e2d)), 1000)}
	if err := r.Notify(t.Context(), long); err != nil {
		t.Fatal(err)
	}
	if n := len(got.Text.Content); n > 2048 || n < 2045 || !utf8.ValidString(got.Text.Content) {
		t.Errorf("sent %d bytes, valid %v", n, utf8.ValidString(got.Text.Content))
	}
}

func TestNotifyErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
		delay     time.Duration
	}{
		{200, `{"errcode":93000,"errmsg":"invalid webhook url, hint: [1700000000_1_abc]"}`, i18n.WecomErrorKey, true, 0},
		{200, `{"errcode":45009,"errmsg":"api freq out of limit"}`, i18n.WecomErrorThrottled, false, time.Minute},
		{200, `{"errcode":40008,"errmsg":"invalid message type"}`, i18n.WecomErrorApi, true, 0},
		{502, `<html>bad gateway</html>`, i18n.WecomErrorStatus, false, 0},
		{404, `{}`, i18n.WecomErrorStatus, true, 0},
	} {
		m := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		})
		err := open(t, m, nil, map[string]string{"webhook": m.base + "?key=" + key}).Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
		if d, ok := retry.Delay(err); c.delay != 0 && (!ok || d != c.delay) {
			t.Errorf("%s: delay %v %v", c.body, d, ok)
		}
	}
}

func TestWebhook(t *testing.T) {
	m := New()
	for _, webhook := range []string{
		"",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send",
		"https://example.com/cgi-bin/webhook/send?key=" + key,
		"http://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=" + key,
	} {
		err := open(t, m, nil, map[string]string{"webhook": webhook}).Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.WecomErrorWebhook || !retry.IsPermanent(err) {
			t.Errorf("%q: %v", webhook, err)
		}
	}
}

func TestNotifyNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	m := New()
	m.client, m.base = &http.Client{Timeout: 10 * time.Second}, srv.URL+"/cgi-bin/webhook/send"
	err := open(t, m, nil, map[string]string{"webhook": m.base + "?key=" + key}).Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.WecomErrorNetwork || retry.IsPermanent(err) {
		t.Fatalf("no network: %v", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("the error shows the key: %v", err)
	}
}

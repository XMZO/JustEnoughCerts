package dingtalk

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

const (
	token  = "0123abcd-robot-token"
	secret = "SEC0123456789abcdef"
	now    = 1700000000123
)

// serve starts a stand-in for DingTalk and a module that reaches it.
func serve(t *testing.T, h http.HandlerFunc) *Module {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	m := New()
	m.client, m.base = srv.Client(), srv.URL+"/robot/send"
	m.now = func() time.Time { return time.UnixMilli(now) }
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
	var query url.Values
	var got struct {
		Type string `json:"msgtype"`
		Text struct {
			Content string `json:"content"`
		} `json:"text"`
	}
	m := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/robot/send" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		query = r.URL.Query()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	})
	webhook := m.base + "?access_token=" + token

	r := open(t, m, map[string]string{contracts.NoteLocale: "en-US"}, map[string]string{"webhook": " " + webhook + " "})
	if err := r.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	if got.Type != "text" || got.Text.Content != "[JEC] example.com failed\n\nline 1\nline 2" {
		t.Errorf("sent %+v", got)
	}
	if query.Get("access_token") != token || query.Has("timestamp") || query.Has("sign") {
		t.Errorf("query %v", query)
	}
	if err := r.Close(); err != nil {
		t.Error(err)
	}

	// Signed: the time in milliseconds and the secret, keyed by the secret.
	r = open(t, m, map[string]string{contracts.NoteLocale: "zh-CN"}, map[string]string{"webhook": webhook, "secret": secret})
	if err := r.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("1700000000123\n" + secret))
	if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); query.Get("access_token") != token || query.Get("timestamp") != "1700000000123" || query.Get("sign") != want {
		t.Errorf("query %v, want sign %s", query, want)
	}
	// The title is tagged in the language of the channel.
	if prefix := string(rune(0x3010)) + "JEC" + string(rune(0x3011)) + "example.com failed"; !strings.HasPrefix(got.Text.Content, prefix) {
		t.Errorf("content %q", got.Text.Content)
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
		{200, `{"errcode":310000,"errmsg":"keywords not in content"}`, i18n.DingtalkErrorSecurity, true, 0},
		{200, `{"errcode":310000,"errmsg":"sign not match"}`, i18n.DingtalkErrorSecurity, true, 0},
		{200, `{"errcode":300001,"errmsg":"token is not exist"}`, i18n.DingtalkErrorToken, true, 0},
		{200, `{"errcode":300005,"errmsg":"token is not exist"}`, i18n.DingtalkErrorToken, true, 0},
		{200, `{"errcode":410100,"errmsg":"send too fast"}`, i18n.DingtalkErrorThrottled, false, time.Minute},
		{200, `{"errcode":40035,"errmsg":"missing parameter"}`, i18n.DingtalkErrorApi, true, 0},
		{502, `<html>bad gateway</html>`, i18n.DingtalkErrorStatus, false, 0},
		{404, `{"message":"not found"}`, i18n.DingtalkErrorStatus, true, 0},
	} {
		m := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		})
		err := open(t, m, nil, map[string]string{"webhook": m.base + "?access_token=" + token}).Notify(t.Context(), note)
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
		"https://oapi.dingtalk.com/robot/send",
		"https://example.com/robot/send?access_token=" + token,
		"http://oapi.dingtalk.com/robot/send?access_token=" + token,
	} {
		err := open(t, m, nil, map[string]string{"webhook": webhook}).Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.DingtalkErrorWebhook || !retry.IsPermanent(err) {
			t.Errorf("%q: %v", webhook, err)
		}
	}
}

func TestNotifyNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	m := New()
	m.client, m.base = &http.Client{Timeout: 10 * time.Second}, srv.URL+"/robot/send"
	err := open(t, m, nil, map[string]string{"webhook": m.base + "?access_token=" + token}).Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.DingtalkErrorNetwork || retry.IsPermanent(err) {
		t.Fatalf("no network: %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the error shows the token: %v", err)
	}
}

func TestContent(t *testing.T) {
	long := contracts.Note{Title: "t", Text: strings.Repeat("ab"+string(rune(0x4e2d)), 2000)}
	text := content(i18n.BaseLocale, long)
	if len(text) > maxContent || len(text) < maxContent-3 || !utf8.ValidString(text) {
		t.Errorf("%d bytes, valid %v", len(text), utf8.ValidString(text))
	}
	if got := content(i18n.BaseLocale, contracts.Note{Title: "t"}); got != "[JEC] t" {
		t.Errorf("no text: %q", got)
	}
}

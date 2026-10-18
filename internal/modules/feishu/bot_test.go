package feishu

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

const (
	token  = "0123abcd-bot-token"
	secret = "s3cret-of-the-bot"
)

// serve starts a stand-in for Feishu, with the webhooks of Feishu under
// /feishu/ and those of Lark under /lark/, and a module that reaches it.
func serve(t *testing.T, h http.HandlerFunc) *Module {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	m := New()
	m.client, m.bases = srv.Client(), []string{srv.URL + "/feishu/hook/", srv.URL + "/lark/hook/"}
	m.now = func() time.Time { return time.Unix(1700000000, 0) }
	return m
}

func open(t *testing.T, m *Module, config, secrets map[string]string) *bot {
	t.Helper()
	p, err := m.factory(t.Context(), providers.Instance{Config: config, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*bot)
}

var note = contracts.Note{Event: contracts.CertificateIssueFailed, Title: "example.com failed", Subject: "[JEC] example.com failed", Text: "line 1\nline 2"}

// message is what the bot sends.
type message struct {
	Type    string `json:"msg_type"`
	Content struct {
		Text string `json:"text"`
	} `json:"content"`
	Timestamp *string `json:"timestamp"`
	Sign      *string `json:"sign"`
}

func TestNotify(t *testing.T) {
	var path string
	var got message
	m := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		path, got = r.URL.Path, message{}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"code":0,"data":{},"msg":"success"}`)
	})

	b := open(t, m, nil, map[string]string{"webhook": " " + m.bases[0] + token + " "})
	if err := b.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	if path != "/feishu/hook/"+token || got.Type != "text" || got.Content.Text != "[JEC] example.com failed\n\nline 1\nline 2" || got.Timestamp != nil || got.Sign != nil {
		t.Errorf("sent %s %+v", path, got)
	}
	if err := b.Close(); err != nil {
		t.Error(err)
	}

	// Signed, to Lark: HMAC-SHA256 of nothing, keyed by the time in seconds
	// and the secret.
	b = open(t, m, nil, map[string]string{"webhook": m.bases[1] + token, "secret": secret})
	if err := b.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	want := base64.StdEncoding.EncodeToString(hmac.New(sha256.New, []byte("1700000000\n"+secret)).Sum(nil))
	if path != "/lark/hook/"+token || got.Timestamp == nil || *got.Timestamp != "1700000000" || got.Sign == nil || *got.Sign != want {
		t.Errorf("sent %s %+v, want sign %s", path, got, want)
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
		{200, `{"code":19021,"msg":"sign match fail or timestamp is not within one hour from current time"}`, i18n.FeishuErrorSign, true, 0},
		{200, `{"code":19024,"msg":"Key Words Not Found"}`, i18n.FeishuErrorKeyword, true, 0},
		{200, `{"code":11232,"msg":"frequency limited"}`, i18n.FeishuErrorThrottled, false, time.Minute},
		{200, `{"code":19001,"msg":"param invalid: incoming webhook access token invalid"}`, i18n.FeishuErrorToken, true, 0},
		{400, `{"code":9499,"msg":"Bad Request","data":{}}`, i18n.FeishuErrorApi, true, 0},
		{200, `{"StatusCode":19022,"StatusMessage":"Ip Not Allowed"}`, i18n.FeishuErrorApi, true, 0},
		{502, `<html>bad gateway</html>`, i18n.FeishuErrorStatus, false, 0},
		{404, `{}`, i18n.FeishuErrorStatus, true, 0},
	} {
		m := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		})
		err := open(t, m, nil, map[string]string{"webhook": m.bases[0] + token}).Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
		if d, ok := retry.Delay(err); c.delay != 0 && (!ok || d != c.delay) {
			t.Errorf("%s: delay %v %v", c.body, d, ok)
		}
	}
}

// Older bots answer with StatusCode and StatusMessage.
func TestNotifyOlderAnswer(t *testing.T) {
	m := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"Extra":null,"StatusCode":0,"StatusMessage":"success"}`)
	})
	if err := open(t, m, nil, map[string]string{"webhook": m.bases[0] + token}).Notify(t.Context(), note); err != nil {
		t.Error(err)
	}
}

func TestWebhook(t *testing.T) {
	m := New()
	for _, webhook := range []string{
		"",
		feishuBase,
		"https://example.com/open-apis/bot/v2/hook/" + token,
		"http://open.feishu.cn/open-apis/bot/v2/hook/" + token,
	} {
		err := open(t, m, nil, map[string]string{"webhook": webhook}).Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.FeishuErrorWebhook || !retry.IsPermanent(err) {
			t.Errorf("%q: %v", webhook, err)
		}
	}
}

func TestNotifyNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	m := New()
	m.client, m.bases = &http.Client{Timeout: 10 * time.Second}, []string{srv.URL + "/hook/"}
	err := open(t, m, nil, map[string]string{"webhook": m.bases[0] + token}).Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.FeishuErrorNetwork || retry.IsPermanent(err) {
		t.Fatalf("no network: %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the error shows the token: %v", err)
	}
}

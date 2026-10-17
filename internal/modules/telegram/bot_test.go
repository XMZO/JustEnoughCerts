package telegram

import (
	"context"
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

const token = "123456:s3cret-token"

func open(t *testing.T, api string, config map[string]string) *bot {
	t.Helper()
	cfg := map[string]string{"chat": "-1001234", "api": api}
	for k, v := range config {
		cfg[k] = v
	}
	p, err := factory(t.Context(), providers.Instance{Config: cfg, Secrets: map[string]string{"token": token}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*bot)
}

var note = contracts.Note{Event: contracts.CertificateIssueFailed, Title: "<example.com> failed", Text: "a & b\nline 2"}

func TestNotify(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/bot"+token+"/sendMessage" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer srv.Close()

	b := open(t, srv.URL+"/", nil)
	if err := b.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	if got["chat_id"] != "-1001234" || got["parse_mode"] != "HTML" || got["text"] != "<b>&lt;example.com&gt; failed</b>\n\na &amp; b\nline 2" {
		t.Errorf("sent %v", got)
	}
	if err := b.Close(); err != nil {
		t.Error(err)
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
		{401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, i18n.TelegramErrorToken, true, 0},
		{404, `{"ok":false,"error_code":404,"description":"Not Found"}`, i18n.TelegramErrorToken, true, 0},
		{400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, i18n.TelegramErrorChat, true, 0},
		{403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`, i18n.TelegramErrorForbidden, true, 0},
		{429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`, i18n.TelegramErrorThrottled, false, 7 * time.Second},
		{400, `{"ok":false,"error_code":400,"description":"Bad Request: message is too long"}`, i18n.TelegramErrorApi, true, 0},
		{502, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`, i18n.TelegramErrorApi, false, 0},
		{502, `<html>bad gateway</html>`, i18n.TelegramErrorStatus, false, 0},
		{403, `<html>blocked</html>`, i18n.TelegramErrorStatus, true, 0},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		}))
		err := open(t, srv.URL, nil).Notify(t.Context(), note)
		srv.Close()
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
		if d, ok := retry.Delay(err); c.delay != 0 && (!ok || d != c.delay) {
			t.Errorf("%d: delay %v %v", c.status, d, ok)
		}
	}
}

func TestNotifyNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	err := open(t, url, nil).Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.TelegramErrorNetwork || retry.IsPermanent(err) {
		t.Fatalf("a closed server: %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the error shows the token: %v", err)
	}
}

func TestProxySetting(t *testing.T) {
	_, err := factory(context.Background(), providers.Instance{Config: map[string]string{"chat": "1", "proxy": "127.0.0.1:7890"}})
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.TelegramErrorProxy || !retry.IsPermanent(err) {
		t.Errorf("a proxy without a scheme: %v", err)
	}
	if _, err := factory(context.Background(), providers.Instance{Config: map[string]string{"chat": "1", "proxy": "socks5://127.0.0.1:1080"}}); err != nil {
		t.Errorf("a SOCKS5 proxy: %v", err)
	}
}

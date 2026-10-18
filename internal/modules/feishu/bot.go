package feishu

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// Where the webhooks of the custom bots of Feishu, and of Lark, are.
const (
	feishuBase = "https://open.feishu.cn/open-apis/bot/v2/hook/"
	larkBase   = "https://open.larksuite.com/open-apis/bot/v2/hook/"
)

// maxContent bounds the text of a message, in bytes; Feishu takes more.
const maxContent = 4000

func botType() providers.Type {
	return providers.Type{
		ID: "feishu.bot", Kind: providers.Notifier, Name: i18n.FeishuBotName, Description: i18n.FeishuBotDescription,
		Tags: []string{"china"},
		Fields: append([]providers.Field{
			{Key: "webhook", Label: i18n.FeishuFieldWebhook, Help: i18n.FeishuFieldWebhookHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "secret", Label: i18n.FeishuFieldSecret, Help: i18n.FeishuFieldSecretHelp, Kind: providers.Text, Secret: true},
		}, contracts.NotifierFields()...),
	}
}

// bot sends notes to a Feishu group.
type bot struct {
	m       *Module
	webhook string
	secret  string
}

var _ contracts.Notifier = (*bot)(nil)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	return &bot{m: m, webhook: strings.TrimSpace(in.Value("webhook")), secret: strings.TrimSpace(in.Value("secret"))}, nil
}

// Close has nothing to release: the client is the module's.
func (*bot) Close() error { return nil }

// Notify sends n as a text message: its subject, which has JEC in it for a
// bot that wants a keyword, then its text.
func (b *bot) Notify(ctx context.Context, n contracts.Note) error {
	if !slices.ContainsFunc(b.m.bases, func(base string) bool { return strings.HasPrefix(b.webhook, base) && len(b.webhook) > len(base) }) {
		return retry.Permanent(&contracts.Problem{Key: i18n.FeishuErrorWebhook})
	}
	msg := map[string]any{"msg_type": "text", "content": map[string]string{"text": content(n)}}
	if b.secret != "" {
		// Feishu's signature: HMAC-SHA256 of nothing, keyed by the time in
		// seconds and the secret, in Base64, in the body.
		ts := strconv.FormatInt(b.m.now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(ts+"\n"+b.secret))
		msg["timestamp"], msg["sign"] = ts, base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.webhook, bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.FeishuErrorWebhook})
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.m.client.Do(req)
	if err != nil {
		// The token is in the URL: no error may carry it.
		return &contracts.Problem{Key: i18n.FeishuErrorNetwork, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	// Newer answers have code and msg, older ones StatusCode and
	// StatusMessage.
	var reply struct {
		Code          *int   `json:"code"`
		Message       string `json:"msg"`
		StatusCode    *int   `json:"StatusCode"`
		StatusMessage string `json:"StatusMessage"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply)
	if err == nil && reply.Code == nil && reply.StatusCode == nil {
		err = errNoCode
	}
	if err != nil {
		// Not Feishu, or a proxy in the way.
		p := &contracts.Problem{Key: i18n.FeishuErrorStatus, Params: map[string]any{"status": resp.StatusCode}, Err: err}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return p
		}
		return retry.Permanent(p)
	}
	if reply.Code != nil {
		return explain(*reply.Code, reply.Message)
	}
	return explain(*reply.StatusCode, reply.StatusMessage)
}

var errNoCode = errors.New("the answer has no code")

// explain turns Feishu's answer into an error: one that trying again cannot
// fix, such as a wrong signature, ends the attempts.
func explain(code int, message string) error {
	switch {
	case code == 0:
		return nil
	case code == 19021:
		return retry.Permanent(&contracts.Problem{Key: i18n.FeishuErrorSign})
	case code == 19024:
		return retry.Permanent(&contracts.Problem{Key: i18n.FeishuErrorKeyword})
	case code == 11232:
		return retry.After(&contracts.Problem{Key: i18n.FeishuErrorThrottled}, time.Minute)
	case strings.Contains(strings.ToLower(message), "token"):
		return retry.Permanent(&contracts.Problem{Key: i18n.FeishuErrorToken})
	}
	return retry.Permanent(&contracts.Problem{Key: i18n.FeishuErrorApi, Params: map[string]any{"code": code, "message": message}})
}

// content is the text of the message about n: its subject, then its text.
func content(n contracts.Note) string {
	text := cmp.Or(n.Subject, n.Title)
	if n.Text != "" {
		text += "\n\n" + n.Text
	}
	return truncate(text, maxContent)
}

// truncate cuts s to n bytes at most, between characters.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

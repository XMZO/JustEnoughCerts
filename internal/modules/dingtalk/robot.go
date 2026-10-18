package dingtalk

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// defaultBase is where the webhooks of DingTalk's robots are.
const defaultBase = "https://oapi.dingtalk.com/robot/send"

// maxContent bounds the text of a message, in bytes; DingTalk takes more.
const maxContent = 4000

func robotType() providers.Type {
	return providers.Type{
		ID: "dingtalk.robot", Kind: providers.Notifier, Name: i18n.DingtalkRobotName, Description: i18n.DingtalkRobotDescription,
		Tags: []string{"china"},
		Fields: append([]providers.Field{
			{Key: "webhook", Label: i18n.DingtalkFieldWebhook, Help: i18n.DingtalkFieldWebhookHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "secret", Label: i18n.DingtalkFieldSecret, Help: i18n.DingtalkFieldSecretHelp, Kind: providers.Text, Secret: true},
		}, contracts.NotifierFields()...),
	}
}

// robot sends notes to a DingTalk group.
type robot struct {
	m       *Module
	webhook string
	secret  string
	tag     language.Tag
}

var _ contracts.Notifier = (*robot)(nil)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	tag, err := language.Parse(in.Config[contracts.NoteLocale])
	if err != nil {
		tag = i18n.BaseLocale
	}
	return &robot{m: m, webhook: strings.TrimSpace(in.Value("webhook")), secret: strings.TrimSpace(in.Value("secret")), tag: tag}, nil
}

// Close has nothing to release: the client is the module's.
func (*robot) Close() error { return nil }

// Notify sends n as a text message: its title, tagged with JEC so that a
// robot that wants a keyword gets one, then its text.
func (r *robot) Notify(ctx context.Context, n contracts.Note) error {
	if !strings.HasPrefix(r.webhook, r.m.base+"?") {
		return retry.Permanent(&contracts.Problem{Key: i18n.DingtalkErrorWebhook, Params: map[string]any{"prefix": r.m.base + "?"}})
	}
	target := r.webhook
	if r.secret != "" {
		// DingTalk's signature: HMAC-SHA256 of the time in milliseconds and
		// the secret, keyed by the secret, in Base64, in the query.
		ts := strconv.FormatInt(r.m.now().UnixMilli(), 10)
		mac := hmac.New(sha256.New, []byte(r.secret))
		mac.Write([]byte(ts + "\n" + r.secret))
		target += "&timestamp=" + ts + "&sign=" + url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	}
	body, err := json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": content(r.tag, n)}})
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.DingtalkErrorWebhook, Params: map[string]any{"prefix": r.m.base + "?"}})
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.m.client.Do(req)
	if err != nil {
		// The token is in the URL: no error may carry it.
		return &contracts.Problem{Key: i18n.DingtalkErrorNetwork, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	var reply struct {
		Code    *int   `json:"errcode"`
		Message string `json:"errmsg"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply)
	if err == nil && reply.Code == nil {
		err = errNoCode
	}
	if err != nil {
		// Not DingTalk, or a proxy in the way.
		p := &contracts.Problem{Key: i18n.DingtalkErrorStatus, Params: map[string]any{"status": resp.StatusCode}, Err: err}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return p
		}
		return retry.Permanent(p)
	}
	return explain(*reply.Code, reply.Message)
}

var errNoCode = errors.New("the answer has no errcode")

// explain turns DingTalk's answer into an error: one that trying again
// cannot fix, such as a robot whose security settings refuse the message,
// ends the attempts.
func explain(code int, message string) error {
	switch {
	case code == 0:
		return nil
	case code == 310000:
		// Keywords, signature or IP addresses: errmsg says which.
		return retry.Permanent(&contracts.Problem{Key: i18n.DingtalkErrorSecurity, Params: map[string]any{"message": message}})
	case code == 300001 || strings.Contains(message, "token is not exist"):
		return retry.Permanent(&contracts.Problem{Key: i18n.DingtalkErrorToken})
	case code == 410100 || strings.Contains(message, "too fast"):
		return retry.After(&contracts.Problem{Key: i18n.DingtalkErrorThrottled}, time.Minute)
	}
	return retry.Permanent(&contracts.Problem{Key: i18n.DingtalkErrorApi, Params: map[string]any{"code": code, "message": message}})
}

// content is the text of the message about n, in language tag.
func content(tag language.Tag, n contracts.Note) string {
	text := tagged(tag, n.Title)
	if n.Text != "" {
		text += "\n\n" + n.Text
	}
	return truncate(text, maxContent)
}

// tagged is title marked as JEC's.
func tagged(tag language.Tag, title string) string {
	cat, err := i18n.Default()
	if err != nil {
		return title
	}
	text, err := cat.Format(tag, i18n.KernelNotifyTaggedTitle, i18n.Args{"title": title})
	if err != nil {
		return title
	}
	return text
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

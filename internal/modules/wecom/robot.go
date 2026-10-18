package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

// defaultBase is where the webhooks of WeCom's robots are.
const defaultBase = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send"

// maxContent is the most text WeCom takes in a message, in bytes.
const maxContent = 2048

func robotType() providers.Type {
	return providers.Type{
		ID: "wecom.robot", Kind: providers.Notifier, Name: i18n.WecomRobotName, Description: i18n.WecomRobotDescription,
		Tags: []string{"china"},
		Fields: append([]providers.Field{
			{Key: "webhook", Label: i18n.WecomFieldWebhook, Help: i18n.WecomFieldWebhookHelp, Kind: providers.Text, Secret: true, Required: true},
		}, contracts.NotifierFields()...),
	}
}

// robot sends notes to a WeCom group.
type robot struct {
	m       *Module
	webhook string
	tag     language.Tag
}

var _ contracts.Notifier = (*robot)(nil)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	tag, err := language.Parse(in.Config[contracts.NoteLocale])
	if err != nil {
		tag = i18n.BaseLocale
	}
	return &robot{m: m, webhook: strings.TrimSpace(in.Value("webhook")), tag: tag}, nil
}

// Close has nothing to release: the client is the module's.
func (*robot) Close() error { return nil }

// Notify sends n as a text message: its title, tagged with JEC, then its
// text.
func (r *robot) Notify(ctx context.Context, n contracts.Note) error {
	prefix := r.m.base + "?"
	if !strings.HasPrefix(r.webhook, prefix) {
		return retry.Permanent(&contracts.Problem{Key: i18n.WecomErrorWebhook, Params: map[string]any{"prefix": prefix}})
	}
	body, err := json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": content(r.tag, n)}})
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.webhook, bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.WecomErrorWebhook, Params: map[string]any{"prefix": prefix}})
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.m.client.Do(req)
	if err != nil {
		// The key is in the URL: no error may carry it.
		return &contracts.Problem{Key: i18n.WecomErrorNetwork, Err: httpx.Cause(err)}
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
		// Not WeCom, or a proxy in the way.
		p := &contracts.Problem{Key: i18n.WecomErrorStatus, Params: map[string]any{"status": resp.StatusCode}, Err: err}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return p
		}
		return retry.Permanent(p)
	}
	return explain(*reply.Code, reply.Message)
}

var errNoCode = errors.New("the answer has no errcode")

// explain turns WeCom's answer into an error: one that trying again cannot
// fix, such as a key no robot has, ends the attempts.
func explain(code int, message string) error {
	switch code {
	case 0:
		return nil
	case 93000:
		return retry.Permanent(&contracts.Problem{Key: i18n.WecomErrorKey})
	case 45009:
		// A robot takes 20 messages a minute.
		return retry.After(&contracts.Problem{Key: i18n.WecomErrorThrottled}, time.Minute)
	}
	return retry.Permanent(&contracts.Problem{Key: i18n.WecomErrorApi, Params: map[string]any{"code": code, "message": message}})
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

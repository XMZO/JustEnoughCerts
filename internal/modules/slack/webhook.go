package slack

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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

// maxText bounds the text of a message, in characters, under Slack's
// 40000.
const maxText = 3000

func webhookType() providers.Type {
	return providers.Type{
		ID: "slack.webhook", Kind: providers.Notifier, Name: i18n.SlackWebhookName, Description: i18n.SlackWebhookDescription,
		Fields: append([]providers.Field{
			{Key: "url", Label: i18n.SlackFieldUrl, Help: i18n.SlackFieldUrlHelp, Kind: providers.URL, Secret: true, Required: true},
			{Key: "allow_private", Label: i18n.SlackFieldAllowPrivate, Help: i18n.SlackFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		}, contracts.NotifierFields()...),
	}
}

// webhook posts notes to the channel of an incoming webhook.
type webhook struct {
	client *http.Client
	url    string
	host   string
}

var _ contracts.Notifier = (*webhook)(nil)

// factory reads the webhook URL: Slack's, or a Mattermost's or a
// Rocket.Chat's, on a private address only when that is allowed. Over plain
// HTTP the message would cross the network in the clear, so it has to be
// HTTPS.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Value("url"))
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.SlackErrorUrl})
	}
	client, err := httpx.Client(httpx.Options{PublicOnly: in.Config["allow_private"] != "true"})
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &webhook{client: client, url: u.String(), host: u.Host}, nil
}

// Close lets go of idle connections.
func (w *webhook) Close() error {
	w.client.CloseIdleConnections()
	return nil
}

// escape escapes what Slack reads as markup: &, < and >.
var escape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

// Notify posts n as text: its subject, a blank line and its text, and the
// link to the page in JEC when there is one. Plain text reads the same in
// Slack, Mattermost and Rocket.Chat, whose markup differs.
func (w *webhook) Notify(ctx context.Context, n contracts.Note) error {
	text := escape(cmp.Or(n.Subject, n.Title)) + "\n\n" + escape(n.Text)
	if utf8.RuneCountInString(text) > maxText {
		text = string([]rune(text)[:maxText-1]) + "…"
	}
	if n.Link != "" {
		text += "\n" + n.Link
	}
	body, err := json.Marshal(map[string]any{"text": text, "username": "JEC"})
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		if errors.Is(err, httpx.ErrNotPublic) {
			return retry.Permanent(&contracts.Problem{Key: i18n.SlackErrorPrivate, Params: map[string]any{"host": w.host}})
		}
		// The URL holds the token: only the cause goes on.
		return &contracts.Problem{Key: i18n.SlackErrorNetwork, Params: map[string]any{"host": w.host}, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	return explain(resp)
}

// explain turns the answer into an error: one that trying again cannot
// fix, such as a webhook removed or a channel archived, ends the attempts.
// Slack says why as plain text, such as invalid_token.
func explain(resp *http.Response) error {
	status := resp.StatusCode
	if status/100 == 2 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	reason := strings.TrimSpace(string(raw))
	if len(reason) > 200 || strings.ContainsAny(reason, "<{") {
		reason = http.StatusText(status) // a page, not a reason
	}
	switch status {
	case http.StatusForbidden, http.StatusNotFound, http.StatusGone, http.StatusUnauthorized:
		return retry.Permanent(&contracts.Problem{Key: i18n.SlackErrorWebhook, Params: map[string]any{"reason": reason}})
	case http.StatusTooManyRequests:
		wait, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return retry.After(&contracts.Problem{Key: i18n.SlackErrorApi, Params: map[string]any{"status": status, "reason": reason}}, time.Duration(wait)*time.Second)
	}
	p := &contracts.Problem{Key: i18n.SlackErrorApi, Params: map[string]any{"status": status, "reason": reason}}
	if status >= 500 {
		return p
	}
	return retry.Permanent(p)
}

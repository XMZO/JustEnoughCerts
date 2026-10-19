package discord

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
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

// Bounds of an embed, in characters, as Discord has them.
const (
	maxTitle       = 256
	maxDescription = 4096
)

// Colors of the embeds, the fills of the design system: danger for what
// failed, warn for what needs attention, ok for what went well, the brand
// otherwise.
const (
	colorDanger = 0xE8553E
	colorWarn   = 0xE5A823
	colorOK     = 0x52B548
	colorOther  = 0x2B7A33
)

// hosts are Discord's, which webhook URLs are on.
var hosts = []string{"discord.com", "discordapp.com", "canary.discord.com", "ptb.discord.com"}

func webhookType() providers.Type {
	return providers.Type{
		ID: "discord.webhook", Kind: providers.Notifier, Name: i18n.DiscordWebhookName, Description: i18n.DiscordWebhookDescription, Untested: true,
		Fields: append([]providers.Field{
			{Key: "url", Label: i18n.DiscordFieldUrl, Help: i18n.DiscordFieldUrlHelp, Kind: providers.URL, Secret: true, Required: true},
			httpx.ProxyField(),
		}, contracts.NotifierFields()...),
	}
}

// webhook posts notes to the channel of a webhook.
type webhook struct {
	client *http.Client
	url    string
}

var _ contracts.Notifier = (*webhook)(nil)

// factory reads the webhook URL, which has to be Discord's: the token in it
// goes nowhere else.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Value("url"))
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !slices.Contains(hosts, u.Host) || !strings.HasPrefix(u.Path, "/api/webhooks/") {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.DiscordErrorUrl})
	}
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: true}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	q := u.Query()
	q.Set("wait", "true") // for an answer that tells why it failed
	u.RawQuery = q.Encode()
	return &webhook{client: client, url: u.String()}, nil
}

// Close lets go of idle connections.
func (w *webhook) Close() error {
	w.client.CloseIdleConnections()
	return nil
}

// cut returns s cut to max characters, with an ellipsis when it was longer.
func cut(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit-1]) + "…"
}

// color is the color of the embed of a note about event.
func color(event string) int {
	switch event {
	case contracts.CertificateIssueFailed, contracts.CertificateDeployFailed:
		return colorDanger
	case contracts.CertificateExpiring, contracts.CertificateServedOld:
		return colorWarn
	case contracts.CertificateIssued, contracts.CertificateDeployed:
		return colorOK
	}
	return colorOther
}

// Notify posts n as an embed: its subject as the title, which links to the
// page in JEC when there is one, and its text.
func (w *webhook) Notify(ctx context.Context, n contracts.Note) error {
	embed := map[string]any{"title": cut(cmp.Or(n.Subject, n.Title), maxTitle), "description": cut(n.Text, maxDescription), "color": color(n.Event)}
	if n.Link != "" {
		embed["url"] = n.Link
	}
	if !n.Time.IsZero() {
		embed["timestamp"] = n.Time.UTC().Format(time.RFC3339)
	}
	body, err := json.Marshal(map[string]any{"username": "JEC", "embeds": []any{embed}, "allowed_mentions": map[string]any{"parse": []string{}}})
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
		// The URL holds the token: only the cause goes on.
		return &contracts.Problem{Key: i18n.DiscordErrorNetwork, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	return explain(resp)
}

// explain turns Discord's answer into an error: one that trying again
// cannot fix, such as a webhook deleted, ends the attempts; limits wait as
// long as Discord asks.
func explain(resp *http.Response) error {
	status := resp.StatusCode
	if status/100 == 2 {
		return nil
	}
	var reply struct {
		Message    string  `json:"message"`
		RetryAfter float64 `json:"retry_after"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return retry.Permanent(&contracts.Problem{Key: i18n.DiscordErrorWebhook, Params: map[string]any{"message": reply.Message}})
	case http.StatusTooManyRequests:
		wait := time.Duration(reply.RetryAfter * float64(time.Second))
		if s, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil && wait == 0 {
			wait = time.Duration(s * float64(time.Second))
		}
		return retry.After(&contracts.Problem{Key: i18n.DiscordErrorApi, Params: map[string]any{"status": status, "message": reply.Message}}, wait)
	}
	p := &contracts.Problem{Key: i18n.DiscordErrorApi, Params: map[string]any{"status": status, "message": cmp.Or(reply.Message, http.StatusText(status))}}
	if status >= 500 {
		return p
	}
	return retry.Permanent(p)
}

package googlechat

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
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

// maxText bounds the text of a message, in characters; Google Chat takes
// 4096 bytes and more, but a note needs no more.
const maxText = 4000

// host is Google Chat's, which webhook URLs are on.
const host = "chat.googleapis.com"

func webhookType() providers.Type {
	return providers.Type{
		ID: "googlechat.webhook", Kind: providers.Notifier, Name: i18n.GooglechatWebhookName, Description: i18n.GooglechatWebhookDescription, Untested: true,
		Fields: append([]providers.Field{
			{Key: "url", Label: i18n.GooglechatFieldUrl, Help: i18n.GooglechatFieldUrlHelp, Kind: providers.URL, Secret: true, Required: true},
			httpx.ProxyField(),
		}, contracts.NotifierFields()...),
	}
}

// webhook posts notes to the space of a webhook.
type webhook struct {
	client *http.Client
	url    string
}

var _ contracts.Notifier = (*webhook)(nil)

// factory reads the webhook URL, which has to be Google Chat's: the key and
// token in it go nowhere else.
func factory(_ context.Context, in providers.Instance) (any, error) {
	u, err := url.Parse(strings.TrimSpace(in.Value("url")))
	if err != nil || u.Scheme != "https" || u.Host != host || !strings.HasPrefix(u.Path, "/v1/spaces/") || !strings.HasSuffix(u.Path, "/messages") {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.GooglechatErrorUrl})
	}
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: true}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &webhook{client: client, url: u.String()}, nil
}

// Close lets go of idle connections.
func (w *webhook) Close() error {
	w.client.CloseIdleConnections()
	return nil
}

// message is the text of n: its subject in bold, its text, and the link to
// the page in JEC on a line of its own, which Chat makes a link.
func message(n contracts.Note) string {
	text := n.Text
	if utf8.RuneCountInString(text) > maxText {
		text = string([]rune(text)[:maxText-1]) + "…"
	}
	parts := []string{"*" + strings.ReplaceAll(cmp.Or(n.Subject, n.Title), "*", "") + "*"}
	if text != "" {
		parts = append(parts, text)
	}
	if n.Link != "" {
		parts = append(parts, n.Link)
	}
	return strings.Join(parts, "\n")
}

// Notify posts n.
func (w *webhook) Notify(ctx context.Context, n contracts.Note) error {
	body, err := json.Marshal(map[string]any{"text": message(n)})
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(err)
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	resp, err := w.client.Do(req)
	if err != nil {
		// The URL holds the key and token: only the cause goes on.
		return &contracts.Problem{Key: i18n.GooglechatErrorNetwork, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	return explain(resp)
}

// explain turns Google's answer into an error: one that trying again cannot
// fix, such as a webhook deleted, ends the attempts.
func explain(resp *http.Response) error {
	status := resp.StatusCode
	if status/100 == 2 {
		return nil
	}
	var reply struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&reply)
	p := &contracts.Problem{Key: i18n.GooglechatErrorApi, Params: map[string]any{"status": status, "message": cmp.Or(reply.Error.Message, reply.Error.Status, http.StatusText(status))}}
	switch {
	case status == http.StatusTooManyRequests:
		s, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return retry.After(p, time.Duration(s)*time.Second)
	case status >= 500:
		return p
	}
	return retry.Permanent(p)
}

package pushover

import (
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// endpoint is Pushover's API for messages.
const endpoint = "https://api.pushover.net/1/messages.json"

// Bounds of a message, in characters, as Pushover has them.
const (
	maxTitle   = 250
	maxMessage = 1024
)

func userType() providers.Type {
	return providers.Type{
		ID: "pushover.user", Kind: providers.Notifier, Name: i18n.PushoverUserName, Description: i18n.PushoverUserDescription,
		Fields: append([]providers.Field{
			{Key: "token", Label: i18n.PushoverFieldToken, Help: i18n.PushoverFieldTokenHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "user", Label: i18n.PushoverFieldUser, Help: i18n.PushoverFieldUserHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "device", Label: i18n.PushoverFieldDevice, Help: i18n.PushoverFieldDeviceHelp, Kind: providers.Text, Advanced: true},
		}, contracts.NotifierFields()...),
	}
}

// user pushes notes to the devices of a Pushover user or group.
type user struct {
	client              *http.Client
	endpoint            string
	token, user, device string
}

var _ contracts.Notifier = (*user)(nil)

func factory(_ context.Context, in providers.Instance) (any, error) {
	client, err := httpx.Client(httpx.Options{PublicOnly: true})
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &user{client: client, endpoint: endpoint, token: strings.TrimSpace(in.Value("token")), user: strings.TrimSpace(in.Value("user")),
		device: strings.TrimSpace(in.Config["device"])}, nil
}

// Close lets go of idle connections.
func (u *user) Close() error {
	u.client.CloseIdleConnections()
	return nil
}

func cut(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
}

// Notify pushes n: its subject as the title and its text as the message,
// with high priority for what needs attention; tapping it opens the page
// in JEC, when there is one.
func (u *user) Notify(ctx context.Context, n contracts.Note) error {
	form := url.Values{"token": {u.token}, "user": {u.user}, "title": {cut(cmp.Or(n.Subject, n.Title), maxTitle)}, "message": {cut(n.Text, maxMessage)}}
	if u.device != "" {
		form.Set("device", u.device)
	}
	if n.Link != "" {
		form.Set("url", n.Link)
		form.Set("url_title", "JEC")
	}
	switch n.Event {
	case contracts.CertificateIssueFailed, contracts.CertificateDeployFailed, contracts.CertificateExpiring, contracts.CertificateServedOld:
		form.Set("priority", "1")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return retry.Permanent(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := u.client.Do(req)
	if err != nil {
		return &contracts.Problem{Key: i18n.PushoverErrorNetwork, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	return explain(resp)
}

// explain turns Pushover's answer into an error: one that trying again
// cannot fix, such as a token or a user key it does not know, ends the
// attempts. It says why in errors, and which key it took for bad.
func explain(resp *http.Response) error {
	status := resp.StatusCode
	var reply struct {
		Status int      `json:"status"`
		Errors []string `json:"errors"`
		Token  string   `json:"token"`
		User   string   `json:"user"`
	}
	decoded := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply) == nil
	switch {
	case status == http.StatusOK && decoded && reply.Status == 1:
		return nil
	case !decoded:
		p := &contracts.Problem{Key: i18n.PushoverErrorStatus, Params: map[string]any{"status": status}}
		if status >= 500 {
			return p
		}
		return retry.Permanent(p)
	case reply.Token == "invalid" || reply.User == "invalid":
		return retry.Permanent(&contracts.Problem{Key: i18n.PushoverErrorKeys, Params: map[string]any{"message": strings.Join(reply.Errors, "; ")}})
	}
	p := &contracts.Problem{Key: i18n.PushoverErrorApi, Params: map[string]any{"status": status, "message": strings.Join(reply.Errors, "; ")}}
	if status >= 500 || status == http.StatusTooManyRequests {
		return p
	}
	return retry.Permanent(p)
}

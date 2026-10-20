package onebot

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
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// maxText bounds a message, in characters; QQ cuts long ones short.
const maxText = 3000

// Whom a message goes to.
const (
	toGroup   = "group"
	toPrivate = "private"
)

func botType() providers.Type {
	return providers.Type{
		ID: "onebot.qq", Kind: providers.Notifier, Name: i18n.OnebotQqName, Description: i18n.OnebotQqDescription, Untested: true,
		Tags: []string{"self_hosted"},
		Fields: append([]providers.Field{
			{Key: "server", Label: i18n.OnebotFieldServer, Help: i18n.OnebotFieldServerHelp, Kind: providers.URL, Required: true},
			{Key: "token", Label: i18n.OnebotFieldToken, Help: i18n.OnebotFieldTokenHelp, Kind: providers.Text, Secret: true},
			{Key: "to", Label: i18n.OnebotFieldTo, Kind: providers.Select, Required: true, Default: toGroup, Options: []providers.Option{
				{Value: toGroup, Label: i18n.OnebotToGroup},
				{Value: toPrivate, Label: i18n.OnebotToPrivate},
			}},
			{Key: "target", Label: i18n.OnebotFieldTarget, Help: i18n.OnebotFieldTargetHelp, Kind: providers.Text, Required: true},
			{Key: "allow_private", Label: i18n.OnebotFieldAllowPrivate, Help: i18n.OnebotFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false"},
		}, contracts.NotifierFields()...),
	}
}

// bot sends notes through the HTTP API of a OneBot implementation.
type bot struct {
	client *http.Client
	server *url.URL
	token  string
	to     string
	target int64
}

var _ contracts.Notifier = (*bot)(nil)

// factory reads the settings of in. Only public addresses are reached
// unless the instance allows private ones, as a bot on the same host does.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["server"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.OnebotErrorServer, Params: map[string]any{"server": raw}})
	}
	target, err := strconv.ParseInt(strings.TrimSpace(in.Config["target"]), 10, 64)
	if err != nil || target <= 0 {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.OnebotErrorTarget})
	}
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: in.Config["allow_private"] != "true"}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	to := toGroup
	if in.Config["to"] == toPrivate {
		to = toPrivate
	}
	return &bot{client: client, server: u, token: strings.TrimSpace(in.Value("token")), to: to, target: target}, nil
}

// Close lets go of idle connections.
func (b *bot) Close() error {
	b.client.CloseIdleConnections()
	return nil
}

// message is the plain text of n: its subject, its text and the link to the
// page in JEC.
func message(n contracts.Note) string {
	text := n.Text
	if utf8.RuneCountInString(text) > maxText {
		text = string([]rune(text)[:maxText-1]) + "…"
	}
	parts := []string{cmp.Or(n.Subject, n.Title)}
	if text != "" {
		parts = append(parts, text)
	}
	if n.Link != "" {
		parts = append(parts, n.Link)
	}
	return strings.Join(parts, "\n")
}

// Notify sends n as plain text, which auto_escape keeps from being read as
// CQ codes.
func (b *bot) Notify(ctx context.Context, n contracts.Note) error {
	action, msg := "/send_group_msg", map[string]any{"group_id": b.target, "message": message(n), "auto_escape": true}
	if b.to == toPrivate {
		action, msg = "/send_private_msg", map[string]any{"user_id": b.target, "message": message(n), "auto_escape": true}
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.server.String()+action, bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.OnebotErrorServer, Params: map[string]any{"server": b.server.Host}})
	}
	req.Header.Set("Content-Type", "application/json")
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		if errors.Is(err, httpx.ErrNotPublic) {
			return retry.Permanent(&contracts.Problem{Key: i18n.OnebotErrorPrivate, Params: map[string]any{"host": b.server.Hostname()}})
		}
		return &contracts.Problem{Key: i18n.OnebotErrorNetwork, Params: map[string]any{"host": b.server.Host}, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	return explain(resp)
}

// explain turns the bot's answer into an error. OneBot answers 401 without
// a token it wants and 403 for a wrong one; a refusal of QQ comes in the
// body.
func explain(resp *http.Response) error {
	switch status := resp.StatusCode; {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return retry.Permanent(&contracts.Problem{Key: i18n.OnebotErrorToken})
	case status == http.StatusNotFound:
		return retry.Permanent(&contracts.Problem{Key: i18n.OnebotErrorApi, Params: map[string]any{"message": http.StatusText(status)}})
	case status/100 != 2:
		p := &contracts.Problem{Key: i18n.OnebotErrorApi, Params: map[string]any{"message": http.StatusText(status)}}
		if status >= 500 {
			return p
		}
		return retry.Permanent(p)
	}
	var reply struct {
		Status  string `json:"status"`
		Retcode int    `json:"retcode"`
		Msg     string `json:"msg"`
		Wording string `json:"wording"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&reply); err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.OnebotErrorApi, Params: map[string]any{"message": err.Error()}})
	}
	if reply.Status == "ok" || reply.Status == "async" || reply.Retcode == 0 && reply.Status == "" {
		return nil
	}
	return retry.Permanent(&contracts.Problem{Key: i18n.OnebotErrorApi, Params: map[string]any{"message": cmp.Or(reply.Wording, reply.Msg, "retcode "+strconv.Itoa(reply.Retcode))}})
}

package gotify

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// The priorities of notes: the Android app makes a heads-up notification
// from 8 on, and a plain one from 4 on.
const (
	priorityHigh   = 8
	priorityNormal = 5
)

func appType() providers.Type {
	return providers.Type{
		ID: "gotify.app", Kind: providers.Notifier, Name: i18n.GotifyAppName, Description: i18n.GotifyAppDescription,
		Tags: []string{"self_hosted"},
		Fields: append([]providers.Field{
			{Key: "server", Label: i18n.GotifyFieldServer, Help: i18n.GotifyFieldServerHelp, Kind: providers.URL, Required: true},
			{Key: "token", Label: i18n.GotifyFieldToken, Help: i18n.GotifyFieldTokenHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "allow_private", Label: i18n.GotifyFieldAllowPrivate, Help: i18n.GotifyFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
			httpx.ProxyField(),
		}, contracts.NotifierFields()...),
	}
}

// app pushes notes as a Gotify application.
type app struct {
	client *http.Client
	server *url.URL
	token  string
}

var _ contracts.Notifier = (*app)(nil)

// factory reads the settings of in. Only public addresses are reached
// unless the instance allows private ones.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["server"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.GotifyErrorServer, Params: map[string]any{"server": raw}})
	}
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: in.Config["allow_private"] != "true"}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	// The token is in a header that a redirect would take along, to
	// anywhere.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &app{client: client, server: u, token: strings.TrimSpace(in.Value("token"))}, nil
}

// Close lets go of idle connections.
func (a *app) Close() error {
	a.client.CloseIdleConnections()
	return nil
}

// Notify pushes n: its title and text, as plain text, with a high priority
// when something needs attention; tapping it opens the page in JEC, when
// there is one.
func (a *app) Notify(ctx context.Context, n contracts.Note) error {
	extras := map[string]any{"client::display": map[string]string{"contentType": "text/plain"}}
	if n.Link != "" {
		extras["client::notification"] = map[string]any{"click": map[string]string{"url": n.Link}}
	}
	body, err := json.Marshal(map[string]any{
		"title": n.Title, "message": cmp.Or(n.Text, n.Title), "priority": priority(n.Event), "extras": extras,
	})
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.server.String()+"/message", bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.GotifyErrorServer, Params: map[string]any{"server": a.server.Host}})
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", a.token)
	resp, err := a.client.Do(req)
	if err != nil {
		if errors.Is(err, httpx.ErrNotPublic) {
			return retry.Permanent(&contracts.Problem{Key: i18n.GotifyErrorPrivate, Params: map[string]any{"host": a.server.Hostname()}})
		}
		return &contracts.Problem{Key: i18n.GotifyErrorNetwork, Params: map[string]any{"host": a.server.Host}, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	return explain(resp)
}

// priority is that of the notes about event.
func priority(event string) int {
	switch event {
	case contracts.CertificateIssueFailed, contracts.CertificateDeployFailed, contracts.CertificateExpiring, contracts.CertificateServedOld, contracts.CertificateChainIncomplete,
		contracts.CertificateSpotted:
		return priorityHigh
	}
	return priorityNormal
}

// explain turns Gotify's answer into an error: one that trying again
// cannot fix, such as a wrong token, ends the attempts.
func explain(resp *http.Response) error {
	status := resp.StatusCode
	switch {
	case status >= 200 && status < 300:
		return nil
	case status >= 300 && status < 400:
		return retry.Permanent(&contracts.Problem{Key: i18n.GotifyErrorRedirect, Params: map[string]any{"status": status, "location": resp.Header.Get("Location")}})
	}
	var reply struct {
		Error       string `json:"error"`
		Description string `json:"errorDescription"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply); err != nil || reply.Error == "" {
		// Not Gotify, or a proxy in the way.
		p := &contracts.Problem{Key: i18n.GotifyErrorStatus, Params: map[string]any{"status": status}, Err: err}
		if status >= 500 || status == http.StatusTooManyRequests {
			return p
		}
		return retry.Permanent(p)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return retry.Permanent(&contracts.Problem{Key: i18n.GotifyErrorToken})
	}
	p := &contracts.Problem{Key: i18n.GotifyErrorApi, Params: map[string]any{"status": status, "message": cmp.Or(reply.Description, reply.Error)}}
	if status >= 500 || status == http.StatusTooManyRequests {
		return p
	}
	return retry.Permanent(p)
}

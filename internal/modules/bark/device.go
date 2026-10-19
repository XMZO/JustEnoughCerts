package bark

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

// defaultServer is Bark's public server.
const defaultServer = "https://api.day.app"

func deviceType() providers.Type {
	return providers.Type{
		ID: "bark.device", Kind: providers.Notifier, Name: i18n.BarkDeviceName, Description: i18n.BarkDeviceDescription,
		Fields: append([]providers.Field{
			{Key: "key", Label: i18n.BarkFieldKey, Help: i18n.BarkFieldKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "server", Label: i18n.BarkFieldServer, Help: i18n.BarkFieldServerHelp, Kind: providers.URL, Default: defaultServer, Advanced: true},
			{Key: "allow_private", Label: i18n.BarkFieldAllowPrivate, Help: i18n.BarkFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
			httpx.ProxyField(),
		}, contracts.NotifierFields()...),
	}
}

// device pushes notes to the iPhone of a device key.
type device struct {
	client *http.Client
	server *url.URL
	key    string
}

var _ contracts.Notifier = (*device)(nil)

// factory reads the settings of in. Only public addresses are reached
// unless the instance allows private ones.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := cmp.Or(strings.TrimSpace(in.Config["server"]), defaultServer)
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.BarkErrorServer, Params: map[string]any{"server": raw}})
	}
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: in.Config["allow_private"] != "true"}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	// A redirect would turn the POST into a GET, and may lead anywhere.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &device{client: client, server: u, key: strings.TrimSpace(in.Value("key"))}, nil
}

// Close lets go of idle connections.
func (d *device) Close() error {
	d.client.CloseIdleConnections()
	return nil
}

// Notify pushes n: its subject as the title and its text as the body, in
// the group JEC; tapping it opens the page in JEC, when there is one.
func (d *device) Notify(ctx context.Context, n contracts.Note) error {
	msg := map[string]any{"device_key": d.key, "title": cmp.Or(n.Subject, n.Title), "body": n.Text, "group": "JEC"}
	if n.Link != "" {
		msg["url"] = n.Link
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.server.String()+"/push", bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.BarkErrorServer, Params: map[string]any{"server": d.server.Host}})
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := d.client.Do(req)
	if err != nil {
		if errors.Is(err, httpx.ErrNotPublic) {
			return retry.Permanent(&contracts.Problem{Key: i18n.BarkErrorPrivate, Params: map[string]any{"host": d.server.Hostname()}})
		}
		return &contracts.Problem{Key: i18n.BarkErrorNetwork, Params: map[string]any{"host": d.server.Host}, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	status := resp.StatusCode
	if status >= 300 && status < 400 {
		return retry.Permanent(&contracts.Problem{Key: i18n.BarkErrorRedirect, Params: map[string]any{"status": status, "location": resp.Header.Get("Location")}})
	}
	var reply struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply)
	if err == nil && reply.Code == nil {
		err = errNoCode
	}
	if err != nil {
		// Not Bark, or a proxy in the way.
		p := &contracts.Problem{Key: i18n.BarkErrorStatus, Params: map[string]any{"status": status}, Err: err}
		if status >= 500 || status == http.StatusTooManyRequests {
			return p
		}
		return retry.Permanent(p)
	}
	return explain(status, *reply.Code, reply.Message)
}

var errNoCode = errors.New("the answer has no code")

// explain turns Bark's answer into an error: one that trying again cannot
// fix, such as a key the server does not know, ends the attempts.
func explain(status, code int, message string) error {
	switch {
	case status == http.StatusOK && code == http.StatusOK:
		return nil
	case strings.Contains(message, "device token"), strings.Contains(message, "device key"):
		// "failed to get device token": no device has the key.
		return retry.Permanent(&contracts.Problem{Key: i18n.BarkErrorKey})
	}
	p := &contracts.Problem{Key: i18n.BarkErrorApi, Params: map[string]any{"code": code, "message": message}}
	if status >= 500 || status == http.StatusTooManyRequests {
		return p
	}
	return retry.Permanent(p)
}

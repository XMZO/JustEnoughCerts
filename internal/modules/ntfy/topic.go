package ntfy

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// defaultServer is the public server of ntfy.
const defaultServer = "https://ntfy.sh"

// maxMessage bounds the text of a message, in bytes: ntfy makes one of
// more than 4096 an attachment.
const maxMessage = 4000

// topicPattern is what ntfy takes as the name of a topic.
var topicPattern = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

func topicType() providers.Type {
	return providers.Type{
		ID: "ntfy.topic", Kind: providers.Notifier, Name: i18n.NtfyTopicName, Description: i18n.NtfyTopicDescription,
		Tags: []string{"self_hosted"},
		Fields: append([]providers.Field{
			{Key: "server", Label: i18n.NtfyFieldServer, Help: i18n.NtfyFieldServerHelp, Kind: providers.URL, Default: defaultServer, Required: true},
			{Key: "topic", Label: i18n.NtfyFieldTopic, Help: i18n.NtfyFieldTopicHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "token", Label: i18n.NtfyFieldToken, Help: i18n.NtfyFieldTokenHelp, Kind: providers.Text, Secret: true},
			{Key: "allow_private", Label: i18n.NtfyFieldAllowPrivate, Help: i18n.NtfyFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
			httpx.ProxyField(),
		}, contracts.NotifierFields()...),
	}
}

// topic publishes notes to a topic.
type topic struct {
	client *http.Client
	server *url.URL
	name   string
	token  string
}

var _ contracts.Notifier = (*topic)(nil)

// factory reads the settings of in. Only public addresses are reached
// unless the instance allows private ones.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := cmp.Or(strings.TrimSpace(in.Config["server"]), defaultServer)
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.NtfyErrorServer, Params: map[string]any{"server": raw}})
	}
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: in.Config["allow_private"] != "true"}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	// A redirect would turn the POST into a GET, and may lead anywhere.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &topic{client: client, server: u, name: strings.TrimSpace(in.Value("topic")), token: strings.TrimSpace(in.Value("token"))}, nil
}

// Close lets go of idle connections.
func (t *topic) Close() error {
	t.client.CloseIdleConnections()
	return nil
}

// Notify publishes n: its subject as the title and its text as the
// message, more urgent and with a warning sign when something needs
// attention; tapping it opens the page in JEC, when there is one.
func (t *topic) Notify(ctx context.Context, n contracts.Note) error {
	if !topicPattern.MatchString(t.name) {
		return retry.Permanent(&contracts.Problem{Key: i18n.NtfyErrorTopic})
	}
	title := cmp.Or(n.Subject, n.Title)
	msg := map[string]any{"topic": t.name, "title": title, "message": truncate(cmp.Or(n.Text, title), maxMessage)}
	if priority, tag := style(n.Event); tag != "" {
		msg["priority"], msg["tags"] = priority, []string{tag}
	}
	if n.Link != "" {
		msg["click"] = n.Link
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return retry.Permanent(err)
	}
	// JSON goes to the root of the server, not to the topic.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.server.String()+"/", bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.NtfyErrorServer, Params: map[string]any{"server": t.server.Host}})
	}
	req.Header.Set("Content-Type", "application/json")
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		if errors.Is(err, httpx.ErrNotPublic) {
			return retry.Permanent(&contracts.Problem{Key: i18n.NtfyErrorPrivate, Params: map[string]any{"host": t.server.Hostname()}})
		}
		return &contracts.Problem{Key: i18n.NtfyErrorNetwork, Params: map[string]any{"host": t.server.Host}, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	return explain(resp, time.Now())
}

// style is the priority and the tag, an emoji, of the notes about event.
func style(event string) (int, string) {
	switch event {
	case contracts.CertificateIssueFailed, contracts.CertificateDeployFailed, contracts.CertificateExpiring, contracts.CertificateServedOld, contracts.CertificateChainIncomplete,
		contracts.CertificateSpotted:
		return 4, "warning"
	case contracts.CertificateIssued, contracts.CertificateDeployed:
		return 3, "white_check_mark"
	}
	return 3, ""
}

// explain turns ntfy's answer into an error: one that trying again cannot
// fix, such as a topic that needs a token, ends the attempts.
func explain(resp *http.Response, now time.Time) error {
	status := resp.StatusCode
	switch {
	case status >= 200 && status < 300:
		return nil
	case status >= 300 && status < 400:
		return retry.Permanent(&contracts.Problem{Key: i18n.NtfyErrorRedirect, Params: map[string]any{"status": status, "location": resp.Header.Get("Location")}})
	}
	var reply struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply); err != nil || reply.Error == "" {
		// Not ntfy, or a proxy in the way.
		p := &contracts.Problem{Key: i18n.NtfyErrorStatus, Params: map[string]any{"status": status}, Err: err}
		if status >= 500 || status == http.StatusTooManyRequests {
			return p
		}
		return retry.Permanent(p)
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return retry.Permanent(&contracts.Problem{Key: i18n.NtfyErrorAuth, Params: map[string]any{"error": reply.Error}})
	case http.StatusTooManyRequests:
		p := &contracts.Problem{Key: i18n.NtfyErrorThrottled, Params: map[string]any{"error": reply.Error}}
		if d, ok := retry.ParseRetryAfter(resp.Header.Get("Retry-After"), now); ok {
			return retry.After(p, d)
		}
		return p
	}
	p := &contracts.Problem{Key: i18n.NtfyErrorApi, Params: map[string]any{"status": status, "error": reply.Error}}
	if status >= 500 {
		return p
	}
	return retry.Permanent(p)
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

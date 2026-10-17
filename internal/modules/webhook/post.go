package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// The settings both types share.
var (
	secretField = providers.Field{Key: "secret", Label: i18n.WebhookFieldSecret, Help: i18n.WebhookFieldSecretHelp, Kind: providers.Text, Secret: true}
	headerField = providers.Field{Key: "headers", Label: i18n.WebhookFieldHeaders, Help: i18n.WebhookFieldHeadersHelp, Kind: providers.Multiline, Secret: true, Advanced: true}
	allowField  = providers.Field{Key: "allow_private", Label: i18n.WebhookFieldAllowPrivate, Help: i18n.WebhookFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true}
)

// endpoint is where a webhook posts, and how.
type endpoint struct {
	client  *http.Client
	url     *url.URL
	secret  string
	headers http.Header
}

// newEndpoint reads the shared settings of in. Only public addresses are
// reached unless the instance allows private ones; redirects are not
// followed, so that they cannot lead elsewhere.
func newEndpoint(in providers.Instance) (*endpoint, error) {
	raw := strings.TrimSpace(in.Config["url"])
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.WebhookErrorUrl, Params: map[string]any{"url": raw}})
	}
	headers := http.Header{}
	for line := range strings.Lines(in.Value("headers")) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			// Only the name: the value may be a token.
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.WebhookErrorHeaders, Params: map[string]any{"line": name}})
		}
		headers.Add(name, strings.TrimSpace(value))
	}
	client, err := httpx.Client(httpx.Options{PublicOnly: in.Config["allow_private"] != "true"})
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &endpoint{client: client, url: u, secret: in.Value("secret"), headers: headers}, nil
}

// shown is the URL as logs and messages show it: without credentials,
// query or fragment, which may hold a token.
func (e *endpoint) shown() string {
	return (&url.URL{Scheme: e.url.Scheme, Host: e.url.Host, Path: e.url.Path}).String()
}

// post sends body as JSON for event and returns what a 2xx answer says,
// up to a MiB of it.
func (e *endpoint) post(ctx context.Context, event string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url.String(), bytes.NewReader(body))
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.WebhookErrorUrl, Params: map[string]any{"url": e.shown()}})
	}
	for name, values := range e.headers {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "JEC/"+buildinfo.Get().Version)
	req.Header.Set("X-JEC-Event", event)
	if e.secret != "" {
		mac := hmac.New(sha256.New, []byte(e.secret))
		mac.Write(body)
		req.Header.Set("X-JEC-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := e.client.Do(req)
	if err != nil {
		if errors.Is(err, httpx.ErrNotPublic) {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.WebhookErrorPrivate, Params: map[string]any{"host": e.url.Hostname()}})
		}
		return nil, &contracts.Problem{Key: i18n.WebhookErrorNetwork, Params: map[string]any{"host": e.url.Host}, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	code := resp.StatusCode
	switch {
	case code >= 200 && code < 300:
		if err != nil {
			return nil, &contracts.Problem{Key: i18n.WebhookErrorNetwork, Params: map[string]any{"host": e.url.Host}, Err: err}
		}
		return data, nil
	case code >= 300 && code < 400:
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.WebhookErrorRedirect, Params: map[string]any{"status": code, "location": resp.Header.Get("Location")}})
	case code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500:
		p := &contracts.Problem{Key: i18n.WebhookErrorStatus, Params: map[string]any{"status": code}}
		if d := retryAfter(resp.Header.Get("Retry-After"), time.Now()); d > 0 {
			return nil, retry.After(p, d)
		}
		return nil, p
	}
	return nil, retry.Permanent(&contracts.Problem{Key: i18n.WebhookErrorStatus, Params: map[string]any{"status": code}})
}

// retryAfter reads a Retry-After header: seconds, or a date.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil {
		return time.Duration(max(s, 0)) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0)
	}
	return 0
}

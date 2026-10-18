package baota

import (
	"context"
	"crypto/md5" //nolint:gosec // G501: the panel's API signs requests with MD5
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// pause is how long JEC waits between two websites, for the panel to
// reload the web server; tests shorten it.
var pause = 3 * time.Second

func siteType() providers.Type {
	return providers.Type{
		ID: "baota.site", Kind: providers.Target, Name: i18n.BaotaSiteName, Description: i18n.BaotaSiteDescription,
		Tags: []string{"china", "self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.BaotaFieldUrl, Help: i18n.BaotaFieldUrlHelp, Kind: providers.URL, Required: true},
			{Key: "api_key", Label: i18n.BaotaFieldApiKey, Help: i18n.BaotaFieldApiKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "sites", Label: i18n.BaotaFieldSites, Help: i18n.BaotaFieldSitesHelp, Kind: providers.Text, Required: true},
			{Key: "fingerprint", Label: i18n.BaotaFieldFingerprint, Help: i18n.BaotaFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.BaotaFieldAllowPrivate, Help: i18n.BaotaFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// panel is a BaoTa panel with websites to deploy to.
type panel struct {
	client *http.Client
	base   *url.URL
	key    string
	sites  []string
	now    func() time.Time
}

var (
	_ contracts.Target = (*panel)(nil)
	_ contracts.Tester = (*panel)(nil)
)

// factory reads the settings of in. Over plain HTTP, which carries the
// private key in the clear, JEC reaches private addresses only; over
// HTTPS, public ones unless private ones are allowed, and a certificate of
// the panel's own making when its fingerprint is pinned.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["url"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.BaotaErrorUrl, Params: map[string]any{"url": raw}})
	}
	opts := httpx.Options{Timeout: time.Minute}
	if u.Scheme == "http" {
		opts.PrivateOnly = true
	} else {
		opts.PublicOnly = in.Config["allow_private"] != "true"
		opts.Pin = strings.TrimSpace(in.Config["fingerprint"])
	}
	client, err := httpx.Client(opts)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.BaotaErrorFingerprint, Params: map[string]any{"fingerprint": opts.Pin}})
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var sites []string
	for _, s := range strings.Split(in.Config["sites"], ",") {
		if s = strings.TrimSpace(s); s != "" {
			sites = append(sites, s)
		}
	}
	return &panel{client: client, base: u, key: strings.TrimSpace(in.Value("api_key")), sites: sites, now: time.Now}, nil
}

// Close lets go of idle connections.
func (p *panel) Close() error {
	p.client.CloseIdleConnections()
	return nil
}

// Test asks the panel for its system's figures, which takes the API key
// and an address the panel lets in.
func (p *panel) Test(ctx context.Context) (contracts.Message, error) {
	if _, err := p.call(ctx, "/system?action=GetSystemTotal", url.Values{}); err != nil {
		return contracts.Message{}, p.explain(err)
	}
	return contracts.Message{Key: i18n.BaotaTestOk, Params: map[string]any{"host": p.base.Host}}, nil
}

// Deploy sets the certificate on each website, a few seconds apart. The
// panel keeps one certificate per website, so there is nothing to remove
// afterwards.
func (p *panel) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(p.sites) == 0 {
		return none, retry.Permanent(&contracts.Problem{Key: i18n.BaotaErrorSites})
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("baota: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	chain := string(cryptox.EncodeCertificates(req.Chain...))
	for i, site := range p.sites {
		if i > 0 {
			select {
			case <-ctx.Done():
				return none, ctx.Err()
			case <-time.After(pause):
			}
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.BaotaLogSite, Params: map[string]any{"site": site}})
		form := url.Values{"type": {"0"}, "siteName": {site}, "key": {string(key)}, "csr": {chain}}
		if _, err := p.call(ctx, "/site?action=SetSSL", form); err != nil {
			return none, p.explain(err)
		}
	}
	return none, nil
}

// panelError is an answer of the panel that says it failed.
type panelError struct{ message string }

func (e *panelError) Error() string { return "baota: " + e.message }

// statusError is an answer that is no answer of the API.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("baota: HTTP %d", e.status) }

// call posts form to path, signed as the panel's API wants: the time and
// the MD5 of the time and of the MD5 of the key. It returns the answer.
func (p *panel) call(ctx context.Context, path string, form url.Values) (map[string]any, error) {
	now := strconv.FormatInt(p.now().Unix(), 10)
	form.Set("request_time", now)
	form.Set("request_token", md5hex(now+md5hex(p.key)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base.String()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var answer map[string]any
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &answer) != nil {
		return nil, &statusError{status: resp.StatusCode}
	}
	if ok, set := answer["status"].(bool); set && !ok {
		message, _ := answer["msg"].(string)
		return nil, &panelError{message: message}
	}
	return answer, nil
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // G401: the panel's API signs requests with MD5
	return hex.EncodeToString(sum[:])
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as an address the panel does not let in, ends the
// attempts.
func (p *panel) explain(err error) error {
	host := p.base.Hostname()
	var pe *panelError
	var se *statusError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &pe) && strings.Contains(pe.message, "IP"):
		return retry.Permanent(&contracts.Problem{Key: i18n.BaotaErrorIp, Params: map[string]any{"message": pe.message}, Err: err})
	case errors.As(err, &pe):
		return retry.Permanent(&contracts.Problem{Key: i18n.BaotaErrorApi, Params: map[string]any{"message": pe.message}, Err: err})
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.BaotaErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
		if se.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.BaotaErrorPrivate, Params: map[string]any{"host": host}})
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.BaotaErrorPlain, Params: map[string]any{"host": host}})
	case errors.Is(err, httpx.ErrPin):
		return retry.Permanent(&contracts.Problem{Key: i18n.BaotaErrorPin, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.BaotaErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.BaotaErrorNetwork, Params: map[string]any{"host": p.base.Host}, Err: httpx.Cause(err)}
}

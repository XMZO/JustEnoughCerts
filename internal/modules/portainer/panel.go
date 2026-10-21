package portainer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

func panelType() providers.Type {
	return providers.Type{
		ID: "portainer.ssl", Kind: providers.Target, Name: i18n.PortainerSslName, Description: i18n.PortainerSslDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.PortainerFieldUrl, Help: i18n.PortainerFieldUrlHelp, Kind: providers.URL, Required: true},
			{Key: "api_key", Label: i18n.PortainerFieldApiKey, Help: i18n.PortainerFieldApiKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "fingerprint", Label: i18n.PortainerFieldFingerprint, Help: i18n.PortainerFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.PortainerFieldAllowPrivate, Help: i18n.PortainerFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// panel is a Portainer whose HTTPS certificate JEC keeps.
type panel struct {
	client *http.Client
	opts   httpx.Options // of client
	base   *url.URL      // the API
	host   string
	key    string
}

var (
	_ contracts.Target = (*panel)(nil)
	_ contracts.Tester = (*panel)(nil)
)

// factory reads the settings of in. Over plain HTTP, which carries the
// private key in the clear, JEC reaches private addresses only; over
// HTTPS, public ones unless private ones are allowed, and the certificate
// Portainer makes itself when its fingerprint is pinned.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["url"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.PortainerErrorUrl, Params: map[string]any{"url": raw}})
	}
	opts := httpx.Options{Timeout: time.Minute}
	if u.Scheme == "http" {
		opts.PrivateOnly = true
	} else {
		opts.PublicOnly = in.Config["allow_private"] != "true"
		opts.Pin = strings.TrimSpace(in.Config["fingerprint"])
		// Once Portainer serves the certificate JEC deployed, in place of
		// the one pinned, it is trusted for the panel's name.
		opts.Names = []string{u.Hostname()}
	}
	client, err := httpx.Client(opts)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.PortainerErrorFingerprint, Params: map[string]any{"fingerprint": opts.Pin}})
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &panel{client: client, opts: opts, base: u.JoinPath("api"), host: u.Host, key: strings.TrimSpace(in.Value("api_key"))}, nil
}

// Close lets go of idle connections.
func (p *panel) Close() error {
	p.client.CloseIdleConnections()
	return nil
}

// settings are Portainer's HTTPS settings.
type settings struct {
	SelfSigned  bool `json:"selfSigned"`
	HTTPEnabled bool `json:"httpEnabled"`
}

// Test reads the HTTPS settings with the key, changing nothing.
func (p *panel) Test(ctx context.Context) (contracts.Message, error) {
	var s settings
	if err := p.call(ctx, p.client, http.MethodGet, "ssl", nil, &s); err != nil {
		return contracts.Message{}, p.explain(err)
	}
	if s.SelfSigned {
		return contracts.Message{Key: i18n.PortainerTestSelfSigned}, nil
	}
	return contracts.Message{Key: i18n.PortainerTestOk}, nil
}

// Deploy makes the certificate the one Portainer serves, at once. There is
// one, overwritten, so nothing to remove afterwards.
func (p *panel) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("portainer: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	client := p.client
	if p.opts.Pin != "" {
		// Portainer may serve an earlier version, for the same names, even
		// when reached by its address.
		opts := p.opts
		opts.Names = append(slices.Clone(opts.Names), cryptox.Names(req.Chain[0])...)
		if client, err = httpx.Client(opts); err != nil {
			return none, retry.Permanent(err)
		}
		defer client.CloseIdleConnections()
		client.CheckRedirect = p.client.CheckRedirect
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.PortainerLogPut})
	body := map[string]any{"cert": string(cryptox.EncodeCertificates(req.Chain...)), "key": string(key)}
	if err := p.call(ctx, client, http.MethodPut, "ssl", body, nil); err != nil {
		return none, p.explain(err)
	}
	return none, nil
}

// apiError is an answer of the API that says it failed.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("portainer: HTTP %d %s", e.status, e.message) }

// call sends body, if any, as JSON to path of the API through client with
// the key, and decodes the answer into out.
func (p *panel) call(ctx context.Context, client *http.Client, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base.JoinPath(path).String(), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-API-Key", p.key)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var answer struct {
			Message string `json:"message"`
			Details string `json:"details"`
		}
		_ = json.Unmarshal(raw, &answer)
		message := answer.Message
		if answer.Details != "" && answer.Details != answer.Message {
			message += ": " + answer.Details
		}
		return &apiError{status: resp.StatusCode, message: message}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a refused key, ends the attempts.
func (p *panel) explain(err error) error {
	var ae *apiError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &ae) && (ae.status == http.StatusUnauthorized || ae.status == http.StatusForbidden):
		return retry.Permanent(&contracts.Problem{Key: i18n.PortainerErrorKey, Err: err})
	case errors.As(err, &ae) && ae.message == "":
		problem := &contracts.Problem{Key: i18n.PortainerErrorStatus, Params: map[string]any{"status": ae.status}, Err: err}
		if ae.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.PortainerErrorApi, Params: map[string]any{"message": ae.message}, Err: err}
		if ae.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.PortainerErrorPrivate, Params: map[string]any{"host": p.host}})
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.PortainerErrorPlain, Params: map[string]any{"host": p.host}})
	case errors.Is(err, httpx.ErrPin):
		return retry.Permanent(&contracts.Problem{Key: i18n.PortainerErrorPin, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.PortainerErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.PortainerErrorNetwork, Params: map[string]any{"host": p.host}, Err: httpx.Cause(err)}
}

package cpanel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

func accountType() providers.Type {
	return providers.Type{
		ID: "cpanel.account", Kind: providers.Target, Name: i18n.CpanelAccountName, Description: i18n.CpanelAccountDescription,
		Tags: []string{"self_hosted"}, Testable: true, Untested: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.CpanelFieldUrl, Help: i18n.CpanelFieldUrlHelp, Kind: providers.URL, Required: true},
			{Key: "username", Label: i18n.CpanelFieldUsername, Kind: providers.Text, Required: true},
			{Key: "api_token", Label: i18n.CpanelFieldApiToken, Help: i18n.CpanelFieldApiTokenHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "domains", Label: i18n.CpanelFieldDomains, Help: i18n.CpanelFieldDomainsHelp, Kind: providers.Text},
			{Key: "fingerprint", Label: i18n.CpanelFieldFingerprint, Help: i18n.CpanelFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.CpanelFieldAllowPrivate, Help: i18n.CpanelFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// account is a cPanel account whose websites JEC installs certificates on,
// through UAPI.
type account struct {
	client  *http.Client
	base    *url.URL
	user    string
	token   string
	domains []string
}

var (
	_ contracts.Target = (*account)(nil)
	_ contracts.Tester = (*account)(nil)
)

// factory reads the settings of in, with the rules of the other panels:
// plain HTTP, which carries the private key in the clear, reaches private
// addresses only; HTTPS public ones unless private ones are allowed, and a
// certificate of the server's own making when its fingerprint is pinned.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["url"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.CpanelErrorUrl, Params: map[string]any{"url": raw}})
	}
	opts := httpx.Options{Timeout: time.Minute}
	if u.Scheme == "http" {
		opts.PrivateOnly = true
	} else {
		opts.PublicOnly = in.Config["allow_private"] != "true"
		opts.Pin = strings.TrimSpace(in.Config["fingerprint"])
		opts.Names = []string{u.Hostname()}
	}
	client, err := httpx.Client(opts)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.CpanelErrorFingerprint, Params: map[string]any{"fingerprint": opts.Pin}})
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var domains []string
	for d := range strings.SplitSeq(in.Config["domains"], ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			domains = append(domains, d)
		}
	}
	return &account{client: client, base: u, user: strings.TrimSpace(in.Config["username"]), token: strings.TrimSpace(in.Value("api_token")), domains: domains}, nil
}

// Close lets go of idle connections.
func (a *account) Close() error {
	a.client.CloseIdleConnections()
	return nil
}

// Test lists the websites of the account that have a certificate
// installed, which takes the API token.
func (a *account) Test(ctx context.Context) (contracts.Message, error) {
	var hosts []struct {
		Servername string `json:"servername"`
	}
	if err := a.call(ctx, "SSL/installed_hosts", url.Values{}, &hosts); err != nil {
		return contracts.Message{}, a.explain(err)
	}
	return contracts.Message{Key: i18n.CpanelTestOk, Params: map[string]any{"user": a.user, "count": len(hosts)}}, nil
}

// Deploy installs the certificate on each website: those set, or else the
// one of the certificate's primary domain. cPanel keeps one certificate a
// website, so there is nothing to remove afterwards.
func (a *account) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("cpanel: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	domains := a.domains
	if len(domains) == 0 && len(req.Domains) > 0 {
		domains = []string{strings.TrimPrefix(req.Domains[0], "*.")}
	}
	form := url.Values{
		"cert":     {string(cryptox.EncodeCertificates(req.Chain[0]))},
		"key":      {string(key)},
		"cabundle": {string(cryptox.EncodeCertificates(req.Chain[1:]...))},
	}
	for _, d := range domains {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.CpanelLogInstall, Params: map[string]any{"domain": d}})
		form.Set("domain", d)
		if err := a.call(ctx, "SSL/install_ssl", form, nil); err != nil {
			return none, a.explain(err)
		}
	}
	return none, nil
}

// apiError is an answer of UAPI that says it failed.
type apiError struct{ message string }

func (e *apiError) Error() string { return "cpanel: " + e.message }

// statusError is an answer that is no answer of UAPI.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("cpanel: HTTP %d", e.status) }

// call posts form to the UAPI function fn, as Module/function, with the
// API token, and reads the data of its answer into out, unless nil.
func (a *account) call(ctx context.Context, fn string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base.String()+"/execute/"+fn, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "cpanel "+a.user+":"+a.token)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var answer struct {
		Status int             `json:"status"`
		Errors []string        `json:"errors"`
		Data   json.RawMessage `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &answer) != nil {
		return &statusError{status: resp.StatusCode}
	}
	if answer.Status != 1 {
		return &apiError{message: strings.Join(answer.Errors, " ")}
	}
	if out != nil && len(answer.Data) > 0 && string(answer.Data) != "null" {
		if err := json.Unmarshal(answer.Data, out); err != nil {
			return &statusError{status: resp.StatusCode}
		}
	}
	return nil
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a token cPanel refuses, ends the attempts.
func (a *account) explain(err error) error {
	host := a.base.Hostname()
	var ae *apiError
	var se *statusError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &ae):
		return retry.Permanent(&contracts.Problem{Key: i18n.CpanelErrorApi, Params: map[string]any{"message": ae.message}, Err: err})
	case errors.As(err, &se) && (se.status == http.StatusUnauthorized || se.status == http.StatusForbidden):
		return retry.Permanent(&contracts.Problem{Key: i18n.CpanelErrorToken, Params: map[string]any{"user": a.user}, Err: err})
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.CpanelErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
		if se.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.CpanelErrorPrivate, Params: map[string]any{"host": host}})
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.CpanelErrorPlain, Params: map[string]any{"host": host}})
	case errors.Is(err, httpx.ErrPin):
		return retry.Permanent(&contracts.Problem{Key: i18n.CpanelErrorPin, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.CpanelErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.CpanelErrorNetwork, Params: map[string]any{"host": a.base.Host}, Err: httpx.Cause(err)}
}

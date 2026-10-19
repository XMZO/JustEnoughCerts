package upyun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
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

// consoleBase is UPYUN's console, whose own interface the module uses: the
// open API (api.upyun.com) only reads certificates.
const consoleBase = "https://console.upyun.com"

func cdnType() providers.Type {
	return providers.Type{
		ID: "upyun.cdn", Kind: providers.Target, Name: i18n.UpyunCdnName, Description: i18n.UpyunCdnDescription, Untested: true,
		Tags: []string{"china"}, Testable: true, Experimental: true,
		Fields: []providers.Field{
			{Key: "username", Label: i18n.UpyunFieldUsername, Kind: providers.Text, Required: true},
			{Key: "password", Label: i18n.UpyunFieldPassword, Help: i18n.UpyunFieldPasswordHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "domains", Label: i18n.UpyunFieldDomains, Help: i18n.UpyunFieldDomainsHelp, Kind: providers.Text},
			{Key: "force_https", Label: i18n.UpyunFieldForceHttps, Help: i18n.UpyunFieldForceHttpsHelp, Kind: providers.Switch, Default: "false"},
		},
	}
}

// cdn is the CDN of an UPYUN account.
type cdn struct {
	client     *http.Client
	base       string
	username   string
	password   string
	domains    []string
	forceHTTPS bool
}

var (
	_ contracts.Target = (*cdn)(nil)
	_ contracts.Tester = (*cdn)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	client, err := httpx.Client(httpx.Options{Timeout: time.Minute, PublicOnly: true})
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cdn{client: client, base: consoleBase, username: strings.TrimSpace(in.Config["username"]), password: in.Value("password"),
		domains: split(in.Config["domains"]), forceHTTPS: in.Config["force_https"] == "true"}, nil
}

// split reads a list of domains, separated by commas or spaces.
func split(s string) []string {
	var out []string
	for _, d := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		out = append(out, strings.ToLower(strings.TrimSuffix(d, ".")))
	}
	return out
}

// Close lets go of idle connections.
func (c *cdn) Close() error {
	c.client.CloseIdleConnections()
	return nil
}

// session is a signed-in session of the console.
type session struct {
	c      *cdn
	client *http.Client
}

// signIn signs in to the console with the account's name and password; the
// session lives in the cookies the console sets.
func (c *cdn) signIn(ctx context.Context) (*session, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := *c.client
	client.Jar = jar
	s := &session{c: c, client: &client}
	var out struct {
		Result bool `json:"result"`
	}
	err = s.call(ctx, http.MethodPost, "/accounts/signin/", map[string]string{"username": c.username, "password": c.password}, &out)
	var ae *apiError
	switch {
	case errors.As(err, &ae) && ae.status < 500:
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.UpyunErrorLogin, Params: map[string]any{"message": ae.message}, Err: err})
	case err != nil:
		return nil, explain(err, "")
	case !out.Result:
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.UpyunErrorLogin, Params: map[string]any{"message": ""}})
	}
	return s, nil
}

// served is how a domain serves a certificate, as the console lists them.
type served struct {
	CertificateID string `json:"certificate_id"`
	HTTPS         bool   `json:"https"`
	ForceHTTPS    bool   `json:"force_https"`
}

// certificates lists the certificates set on domain, the one it serves
// marked by HTTPS.
func (s *session) certificates(ctx context.Context, domain string) ([]served, error) {
	var out struct {
		Result []served `json:"result"`
	}
	if err := s.call(ctx, http.MethodGet, "/api/https/services/manager?"+url.Values{"domain": {domain}}.Encode(), nil, &out); err != nil {
		return nil, explain(err, domain)
	}
	return out.Result, nil
}

// Test signs in, and makes sure the account has the accelerated domains
// set, without changing anything.
func (c *cdn) Test(ctx context.Context) (contracts.Message, error) {
	s, err := c.signIn(ctx)
	if err != nil {
		return contracts.Message{}, err
	}
	if len(c.domains) == 0 {
		return contracts.Message{Key: i18n.UpyunTestLogin}, nil
	}
	for _, d := range c.domains {
		if _, err := s.certificates(ctx, d); err != nil {
			return contracts.Message{}, err
		}
	}
	return contracts.Message{Key: i18n.UpyunTestOk, Params: map[string]any{"domains": strings.Join(c.domains, ", ")}}, nil
}

// Deploy uploads the certificate and sets it on each accelerated domain:
// those set, or else the certificate's own, but for wildcards. A domain
// serving another certificate moves to it; one without HTTPS gets HTTPS
// with it, forced if set to. The console has no way to delete the
// certificates of earlier deployments: they stay in UPYUN's store.
func (c *cdn) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("upyun: no certificate to deploy"))
	}
	domains := c.domains
	if len(domains) == 0 {
		for _, d := range req.Domains {
			if !strings.HasPrefix(d, "*.") {
				domains = append(domains, d)
			}
		}
	}
	if len(domains) == 0 {
		return none, retry.Permanent(&contracts.Problem{Key: i18n.UpyunErrorNoDomains})
	}
	s, err := c.signIn(ctx)
	if err != nil {
		return none, err
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.UpyunLogUpload})
	var uploaded struct {
		Result struct {
			CertificateID string `json:"certificate_id"`
		} `json:"result"`
	}
	body := map[string]string{"certificate": string(cryptox.EncodeCertificates(req.Chain...)), "private_key": string(key)}
	if err := s.call(ctx, http.MethodPost, "/api/https/certificate/", body, &uploaded); err != nil {
		return none, explain(err, "")
	}
	id := uploaded.Result.CertificateID
	if id == "" {
		return none, explain(&statusError{status: http.StatusOK}, "")
	}
	for _, d := range domains {
		if err := s.set(ctx, d, id, log); err != nil {
			return none, err
		}
	}
	if len(req.Previous) > 0 {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.UpyunLogKept})
	}
	return contracts.DeployResult{Ref: id}, nil
}

// set makes domain serve certificate id.
func (s *session) set(ctx context.Context, domain, id string, log func(contracts.LogLine)) error {
	list, err := s.certificates(ctx, domain)
	if err != nil {
		return err
	}
	for _, sv := range list {
		if !sv.HTTPS {
			continue
		}
		if sv.CertificateID == id {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.UpyunLogCurrent, Params: map[string]any{"domain": domain}})
			return nil
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.UpyunLogSwitch, Params: map[string]any{"domain": domain}})
		return done(domain, s.call(ctx, http.MethodPost, "/api/https/migrate/domain", map[string]string{"crt_id": id, "domain_name": domain}, nil))
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.UpyunLogEnable, Params: map[string]any{"domain": domain}})
	body := map[string]any{"certificate_id": id, "domain": domain, "https": true, "force_https": s.c.forceHTTPS}
	return done(domain, s.call(ctx, http.MethodPost, "/api/https/certificate/manager", body, nil))
}

// done explains err, the failure to set a certificate on domain, if any:
// the console calls it a failure when the domain has it already.
func done(domain string, err error) error {
	var ae *apiError
	if errors.As(err, &ae) && strings.Contains(ae.message, "has been bound to this certificate") {
		return nil
	}
	if err != nil {
		return explain(err, domain)
	}
	return nil
}

// apiError is an answer of the console that says it failed.
type apiError struct {
	status  int
	code    int64
	message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("upyun: HTTP %d: %d %s", e.status, e.code, e.message)
}

// statusError is an answer that is no answer of the console.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("upyun: HTTP %d", e.status) }

// call sends body as JSON, when not nil, to path of the console, and
// decodes the data of the answer into out. The console wraps what it
// answers in {"data": ...}, with error_code and message when it failed.
func (s *session) call(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.c.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var answer struct {
		Data json.RawMessage `json:"data"`
		// Errors and Msg are how the console reports a request it cannot
		// take at all, as a sign-in without a password.
		Errors []json.RawMessage `json:"errors"`
		Msg    json.RawMessage   `json:"msg"`
	}
	if json.Unmarshal(raw, &answer) != nil {
		return &statusError{status: resp.StatusCode}
	}
	var failed struct {
		ErrorCode json.Number `json:"error_code"`
		Message   string      `json:"message"`
	}
	_ = json.Unmarshal(answer.Data, &failed)
	code, _ := failed.ErrorCode.Int64()
	switch {
	case code != 0 || (resp.StatusCode/100 != 2 && failed.Message != ""):
		return &apiError{status: resp.StatusCode, code: code, message: failed.Message}
	case len(answer.Errors) > 0 || (resp.StatusCode/100 != 2 && len(answer.Msg) > 0):
		return &apiError{status: resp.StatusCode, message: text(answer.Msg)}
	case resp.StatusCode/100 != 2:
		return &statusError{status: resp.StatusCode}
	}
	if out != nil && len(answer.Data) > 0 && json.Unmarshal(answer.Data, out) != nil {
		return &statusError{status: resp.StatusCode}
	}
	return nil
}

// text is what msg says: a string, or else its JSON.
func text(msg json.RawMessage) string {
	var s string
	if json.Unmarshal(msg, &s) == nil {
		return s
	}
	return string(msg)
}

// explain turns an error of a call about domain, if any, into a Problem:
// one that trying again cannot fix ends the attempts.
func explain(err error, domain string) error {
	var ae *apiError
	var se *statusError
	switch {
	case errors.As(err, &ae) && (ae.status == http.StatusUnauthorized || ae.status == http.StatusForbidden):
		return retry.Permanent(&contracts.Problem{Key: i18n.UpyunErrorLogin, Params: map[string]any{"message": ae.message}, Err: err})
	case errors.As(err, &ae) && domain != "":
		problem := &contracts.Problem{Key: i18n.UpyunErrorDomain, Params: map[string]any{"domain": domain, "message": ae.message}, Err: err}
		if ae.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.UpyunErrorApi, Params: map[string]any{"message": ae.message}, Err: err}
		if ae.status >= 500 || ae.status == http.StatusTooManyRequests {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.UpyunErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
		if se.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	}
	return &contracts.Problem{Key: i18n.UpyunErrorNetwork, Err: httpx.Cause(err)}
}

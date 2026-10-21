package onepanel

import (
	"bytes"
	"cmp"
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

// Versions of the panel's API.
const (
	apiV1 = "v1"
	apiV2 = "v2"
)

// maxListed bounds the certificates a test lists.
const maxListed = 20

func certificateType() providers.Type {
	return providers.Type{
		ID: "onepanel.certificate", Kind: providers.Target, Name: i18n.OnepanelCertificateName, Description: i18n.OnepanelCertificateDescription,
		Tags: []string{"china", "self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.OnepanelFieldUrl, Help: i18n.OnepanelFieldUrlHelp, Kind: providers.URL, Required: true},
			{Key: "api_key", Label: i18n.OnepanelFieldApiKey, Help: i18n.OnepanelFieldApiKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "api_version", Label: i18n.OnepanelFieldApiVersion, Kind: providers.Select, Required: true, Default: apiV2, Options: []providers.Option{
				{Value: apiV2, Label: i18n.OnepanelApiV2},
				{Value: apiV1, Label: i18n.OnepanelApiV1},
			}},
			{Key: "certificate", Label: i18n.OnepanelFieldCertificate, Help: i18n.OnepanelFieldCertificateHelp, Kind: providers.Number},
			{Key: "fingerprint", Label: i18n.OnepanelFieldFingerprint, Help: i18n.OnepanelFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.OnepanelFieldAllowPrivate, Help: i18n.OnepanelFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// panel is a 1Panel with a certificate to keep up to date.
type panel struct {
	client *http.Client
	opts   httpx.Options // of client
	base   *url.URL      // the panel and the path of its API
	host   string
	key    string
	id     int64 // the certificate's, or 0 when not set
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
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorUrl, Params: map[string]any{"url": raw}})
	}
	opts := httpx.Options{Timeout: time.Minute}
	if u.Scheme == "http" {
		opts.PrivateOnly = true
	} else {
		opts.PublicOnly = in.Config["allow_private"] != "true"
		opts.Pin = strings.TrimSpace(in.Config["fingerprint"])
		// Should it serve a certificate a CA vouches for its name, as one
		// JEC deployed, that is trusted too.
		opts.Names = []string{u.Hostname()}
	}
	client, err := httpx.Client(opts)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorFingerprint, Params: map[string]any{"fingerprint": opts.Pin}})
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	p := &panel{client: client, opts: opts, host: u.Host, key: strings.TrimSpace(in.Value("api_key")), now: time.Now}
	p.base = u.JoinPath("api", cmp.Or(in.Config["api_version"], apiV2))
	if s := strings.TrimSpace(in.Config["certificate"]); s != "" {
		if p.id, err = strconv.ParseInt(s, 10, 64); err != nil || p.id <= 0 {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorCertificateId, Params: map[string]any{"id": s}})
		}
	}
	return p, nil
}

// Close lets go of idle connections.
func (p *panel) Close() error {
	p.client.CloseIdleConnections()
	return nil
}

// sslCertificate is a certificate of the panel, as it lists them.
type sslCertificate struct {
	ID            int64  `json:"id"`
	PrimaryDomain string `json:"primaryDomain"`
	Domains       string `json:"domains"`
	Description   string `json:"description"`
}

// name is what people know the certificate by: its domains.
func (c sslCertificate) name() string {
	return "#" + strconv.FormatInt(c.ID, 10) + " " + cmp.Or(c.PrimaryDomain, c.Domains)
}

// certificates lists the panel's certificates, the first page of them.
func (p *panel) certificates(ctx context.Context) ([]sslCertificate, error) {
	var page struct {
		Items []sslCertificate `json:"items"`
	}
	err := p.call(ctx, "/websites/ssl/search", map[string]any{"page": 1, "pageSize": 100}, &page)
	return page.Items, err
}

// find returns the certificate set, and the panel's certificates when it is
// not among them.
func (p *panel) find(ctx context.Context) (sslCertificate, []sslCertificate, error) {
	list, err := p.certificates(ctx)
	if err != nil {
		return sslCertificate{}, nil, p.explain(err)
	}
	for _, c := range list {
		if c.ID == p.id {
			return c, list, nil
		}
	}
	return sslCertificate{}, list, nil
}

// listed names the first certificates of list, for a message.
func listed(list []sslCertificate) string {
	var names []string
	for _, c := range list[:min(len(list), maxListed)] {
		names = append(names, c.name())
	}
	return strings.Join(names, ", ")
}

// Test finds the certificate set, or lists the panel's certificates for
// people to pick one, without changing anything.
func (p *panel) Test(ctx context.Context) (contracts.Message, error) {
	found, list, err := p.find(ctx)
	switch {
	case err != nil:
		return contracts.Message{}, err
	case p.id == 0 && len(list) == 0:
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorNoCertificates})
	case p.id == 0:
		return contracts.Message{Key: i18n.OnepanelTestList, Params: map[string]any{"certificates": listed(list)}}, nil
	case found.ID == 0:
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorNoCertificate, Params: map[string]any{"id": p.id, "certificates": listed(list)}})
	}
	return contracts.Message{Key: i18n.OnepanelTestOk, Params: map[string]any{"certificate": found.name()}}, nil
}

// Deploy replaces the content of the certificate set, keeping its
// description, so that the websites using it serve the new version. There
// is one certificate, overwritten, so nothing to remove afterwards.
func (p *panel) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if p.id == 0 {
		return none, retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorCertificateMissing})
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("onepanel: no certificate to deploy"))
	}
	found, list, err := p.find(ctx)
	switch {
	case err != nil:
		return none, err
	case found.ID == 0:
		return none, retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorNoCertificate, Params: map[string]any{"id": p.id, "certificates": listed(list)}})
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.OnepanelLogUpload, Params: map[string]any{"certificate": found.name()}})
	err = p.call(ctx, "/websites/ssl/upload", map[string]any{
		"sslID": p.id, "type": "paste", "certificate": string(cryptox.EncodeCertificates(req.Chain...)), "privateKey": string(key),
		"description": cmp.Or(found.Description, "JEC"),
	}, nil)
	if err != nil {
		return none, p.explain(err)
	}
	return none, nil
}

// apiError is an answer of the panel that says it failed.
type apiError struct {
	code    int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("onepanel: %d %s", e.code, e.message) }

// statusError is an answer that is no answer of the API, with the start
// of its text.
type statusError struct {
	status int
	text   string
}

func (e *statusError) Error() string { return fmt.Sprintf("onepanel: HTTP %d", e.status) }

// call posts body as JSON to path of the API, or gets it when body is nil,
// signed as the panel wants: the time, and the MD5 of 1panel, the key and
// the time. It decodes the data of the answer into out.
func (p *panel) call(ctx context.Context, path string, body any, out any) error {
	method, reader := http.MethodGet, io.Reader(nil)
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		method, reader = http.MethodPost, bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base.String()+path, reader)
	if err != nil {
		return err
	}
	now := strconv.FormatInt(p.now().Unix(), 10)
	sum := md5.Sum([]byte("1panel" + p.key + now)) //nolint:gosec // G401: the panel's API signs requests with MD5
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("1Panel-Token", hex.EncodeToString(sum[:]))
	req.Header.Set("1Panel-Timestamp", now)
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var answer struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &answer) != nil || answer.Code == 0 {
		return &statusError{status: resp.StatusCode, text: string(raw[:min(len(raw), 200)])}
	}
	if answer.Code/100 != 2 {
		return &apiError{code: answer.Code, message: answer.Message}
	}
	if out == nil || len(answer.Data) == 0 || string(answer.Data) == "null" {
		return nil
	}
	return json.Unmarshal(answer.Data, out)
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a refused key, ends the attempts.
func (p *panel) explain(err error) error {
	var ae *apiError
	var se *statusError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &ae) && ae.code == http.StatusUnauthorized:
		return retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorKey, Err: err})
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.OnepanelErrorApi, Params: map[string]any{"code": ae.code, "message": ae.message}, Err: err}
		if ae.code >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &se) && se.status == http.StatusNotFound:
		return retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorVersion, Err: err})
	case errors.As(err, &se) && se.status == http.StatusUnauthorized:
		return retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorKey, Err: err})
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.OnepanelErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
		if se.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorPrivate, Params: map[string]any{"host": p.host}})
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorPlain, Params: map[string]any{"host": p.host}})
	case errors.Is(err, httpx.ErrPin):
		return retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorPin, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.OnepanelErrorNetwork, Params: map[string]any{"host": p.host}, Err: httpx.Cause(err)}
}

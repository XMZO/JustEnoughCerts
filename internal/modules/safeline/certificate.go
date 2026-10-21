package safeline

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

// maxListed bounds the certificates a message lists.
const maxListed = 20

// manual is the type of a certificate uploaded to the firewall, rather
// than one it gets from Let's Encrypt itself.
const manual = 2

func certificateType() providers.Type {
	return providers.Type{
		ID: "safeline.certificate", Kind: providers.Target, Name: i18n.SafelineCertificateName, Description: i18n.SafelineCertificateDescription,
		Tags: []string{"china", "self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.SafelineFieldUrl, Help: i18n.SafelineFieldUrlHelp, Kind: providers.URL, Required: true},
			{Key: "api_token", Label: i18n.SafelineFieldApiToken, Help: i18n.SafelineFieldApiTokenHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "certificate", Label: i18n.SafelineFieldCertificate, Help: i18n.SafelineFieldCertificateHelp, Kind: providers.Number},
			{Key: "fingerprint", Label: i18n.SafelineFieldFingerprint, Help: i18n.SafelineFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.SafelineFieldAllowPrivate, Help: i18n.SafelineFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// firewall is a SafeLine whose certificates JEC keeps up to date.
type firewall struct {
	client *http.Client
	base   *url.URL // the management API
	host   string
	token  string
	id     int64 // the certificate set, 0 to find one by its domains
}

var (
	_ contracts.Target = (*firewall)(nil)
	_ contracts.Tester = (*firewall)(nil)
)

// factory reads the settings of in. Over plain HTTP, which carries the
// private key in the clear, JEC reaches private addresses only; over
// HTTPS, public ones unless private ones are allowed, and a certificate of
// the firewall's own making when its fingerprint is pinned.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["url"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorUrl, Params: map[string]any{"url": raw}})
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
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorFingerprint, Params: map[string]any{"fingerprint": opts.Pin}})
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	f := &firewall{client: client, base: u.JoinPath("api", "open"), host: u.Host, token: strings.TrimSpace(in.Value("api_token"))}
	if s := strings.TrimSpace(in.Config["certificate"]); s != "" {
		if f.id, err = strconv.ParseInt(s, 10, 64); err != nil || f.id <= 0 {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorCertificateId, Params: map[string]any{"id": s}})
		}
	}
	return f, nil
}

// Close lets go of idle connections.
func (f *firewall) Close() error {
	f.client.CloseIdleConnections()
	return nil
}

// certificate is a certificate of the firewall, as it lists them.
type certificate struct {
	ID           int64    `json:"id"`
	Type         int      `json:"type"`
	Domains      []string `json:"domains"`
	RelatedSites []string `json:"related_sites"`
}

// name is what people know the certificate by: its ID and domains.
func (c certificate) name() string {
	return "#" + strconv.FormatInt(c.ID, 10) + " " + strings.Join(c.Domains, ", ")
}

// certificates lists the firewall's certificates.
func (f *firewall) certificates(ctx context.Context) ([]certificate, error) {
	var list struct {
		Nodes []certificate `json:"nodes"`
	}
	if err := f.call(ctx, http.MethodGet, "cert", nil, &list); err != nil {
		return nil, f.explain(err)
	}
	return list.Nodes, nil
}

// pick returns the certificate of list to replace for domains: the one
// set, else the one for the same domains, one in use by a site first;
// none when no certificate is for them.
func (f *firewall) pick(list []certificate, domains []string) (certificate, bool) {
	if f.id != 0 {
		i := slices.IndexFunc(list, func(c certificate) bool { return c.ID == f.id })
		if i < 0 {
			return certificate{}, false
		}
		return list[i], true
	}
	var found certificate
	for _, c := range list {
		if sameNames(c.Domains, domains) && (found.ID == 0 || len(found.RelatedSites) == 0 && len(c.RelatedSites) > 0) {
			found = c
		}
	}
	return found, found.ID != 0
}

// sameNames reports whether a and b hold the same names, in any order and
// case.
func sameNames(a, b []string) bool {
	norm := func(names []string) []string {
		out := make([]string, 0, len(names))
		for _, n := range names {
			out = append(out, strings.ToLower(strings.TrimSuffix(strings.TrimSpace(n), ".")))
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return len(a) > 0 && slices.Equal(norm(a), norm(b))
}

// listed names the first certificates of list, for a message.
func listed(list []certificate) string {
	var names []string
	for _, c := range list[:min(len(list), maxListed)] {
		names = append(names, c.name())
	}
	return strings.Join(names, "; ")
}

// Test reaches the firewall with the token and finds the certificate set,
// or lists those there, without changing anything.
func (f *firewall) Test(ctx context.Context) (contracts.Message, error) {
	list, err := f.certificates(ctx)
	switch {
	case err != nil:
		return contracts.Message{}, err
	case f.id != 0:
		c, ok := f.pick(list, nil)
		if !ok {
			return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorNoCertificate, Params: map[string]any{"id": f.id, "certificates": listed(list)}})
		}
		return contracts.Message{Key: i18n.SafelineTestOk, Params: map[string]any{"certificate": c.name()}}, nil
	case len(list) == 0:
		return contracts.Message{Key: i18n.SafelineTestNone}, nil
	}
	return contracts.Message{Key: i18n.SafelineTestList, Params: map[string]any{"certificates": listed(list)}}, nil
}

// Deploy replaces the content of the certificate set, or of the one for
// the same domains, so that the sites using it serve the new version; with
// none such, it adds the certificate, for people to give to their sites.
// Replaced in place, there is nothing to remove afterwards.
func (f *firewall) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("safeline: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	list, err := f.certificates(ctx)
	if err != nil {
		return none, err
	}
	body := map[string]any{"type": manual, "manual": map[string]string{"crt": string(cryptox.EncodeCertificates(req.Chain...)), "key": string(key)}}
	found, ok := f.pick(list, cryptox.Names(req.Chain[0]))
	switch {
	case !ok && f.id != 0:
		return none, retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorNoCertificate, Params: map[string]any{"id": f.id, "certificates": listed(list)}})
	case ok:
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.SafelineLogReplace, Params: map[string]any{"certificate": found.name()}})
		body["id"] = found.ID
	default:
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.SafelineLogAdd})
	}
	var id json.RawMessage
	if err := f.call(ctx, http.MethodPost, "cert", body, &id); err != nil {
		return none, f.explain(err)
	}
	if !ok {
		log(contracts.LogLine{Level: contracts.Warn, Key: i18n.SafelineLogAdded, Params: map[string]any{"id": strings.Trim(string(id), `"`)}})
	} else if len(found.RelatedSites) == 0 {
		log(contracts.LogLine{Level: contracts.Warn, Key: i18n.SafelineLogUnused, Params: map[string]any{"certificate": found.name()}})
	}
	return none, nil
}

// apiError is an answer of the firewall that says it failed.
type apiError struct {
	status        int
	code, message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("safeline: HTTP %d %s %s", e.status, e.code, e.message)
}

// statusError is an answer that is no answer of the API.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("safeline: HTTP %d", e.status) }

// call sends body, if any, as JSON to path of the API with the token, and
// decodes the data of the answer into out.
func (f *firewall) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, f.base.JoinPath(path).String(), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-SLCE-API-TOKEN", f.token)
	resp, err := f.client.Do(req)
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
		Err  *string         `json:"err"`
		Msg  string          `json:"msg"`
	}
	if json.Unmarshal(raw, &answer) != nil || answer.Err == nil && answer.Data == nil {
		return &statusError{status: resp.StatusCode}
	}
	if answer.Err != nil && *answer.Err != "" || resp.StatusCode >= 300 {
		code := ""
		if answer.Err != nil {
			code = *answer.Err
		}
		return &apiError{status: resp.StatusCode, code: code, message: answer.Msg}
	}
	if out == nil || len(answer.Data) == 0 || string(answer.Data) == "null" {
		return nil
	}
	return json.Unmarshal(answer.Data, out)
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a refused token, ends the attempts.
func (f *firewall) explain(err error) error {
	var ae *apiError
	var se *statusError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &ae) && (ae.code == "login-required" || ae.status == http.StatusUnauthorized || ae.status == http.StatusForbidden):
		return retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorToken, Err: err})
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.SafelineErrorApi, Params: map[string]any{"message": ae.message, "code": ae.code}, Err: err}
		if ae.status >= 500 && ae.code != "internal-error" {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &se) && se.status >= 500:
		return &contracts.Problem{Key: i18n.SafelineErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
	case errors.As(err, &se):
		return retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorStatus, Params: map[string]any{"status": se.status}, Err: err})
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorPrivate, Params: map[string]any{"host": f.host}})
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorPlain, Params: map[string]any{"host": f.host}})
	case errors.Is(err, httpx.ErrPin):
		return retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorPin, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.SafelineErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.SafelineErrorNetwork, Params: map[string]any{"host": f.host}, Err: httpx.Cause(err)}
}

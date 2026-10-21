package apisix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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

// objectID is what an SSL object's ID may hold, as the Admin API takes it.
var objectID = regexp.MustCompile(`^[a-zA-Z0-9-_.]{1,64}$`)

func sslType() providers.Type {
	return providers.Type{
		ID: "apisix.ssl", Kind: providers.Target, Name: i18n.ApisixSslName, Description: i18n.ApisixSslDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.ApisixFieldUrl, Help: i18n.ApisixFieldUrlHelp, Kind: providers.URL, Required: true},
			{Key: "admin_key", Label: i18n.ApisixFieldAdminKey, Help: i18n.ApisixFieldAdminKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "id", Label: i18n.ApisixFieldId, Help: i18n.ApisixFieldIdHelp, Kind: providers.Text, Advanced: true},
			{Key: "fingerprint", Label: i18n.ApisixFieldFingerprint, Help: i18n.ApisixFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.ApisixFieldAllowPrivate, Help: i18n.ApisixFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// gateway is an APISIX whose SSL objects JEC keeps.
type gateway struct {
	client *http.Client
	base   *url.URL // the Admin API
	host   string
	key    string
	id     string // the SSL object, or "" for jec-<certificate ID>
}

var (
	_ contracts.Target = (*gateway)(nil)
	_ contracts.Tester = (*gateway)(nil)
)

// factory reads the settings of in. Over plain HTTP, which carries the
// private key in the clear, JEC reaches private addresses only; over
// HTTPS, public ones unless private ones are allowed, and a certificate of
// the gateway's own making when its fingerprint is pinned.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["url"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.ApisixErrorUrl, Params: map[string]any{"url": raw}})
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
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.ApisixErrorFingerprint, Params: map[string]any{"fingerprint": opts.Pin}})
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	g := &gateway{client: client, base: u.JoinPath("apisix", "admin"), host: u.Host, key: strings.TrimSpace(in.Value("admin_key")), id: strings.TrimSpace(in.Config["id"])}
	if g.id != "" && !objectID.MatchString(g.id) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.ApisixErrorId, Params: map[string]any{"id": g.id}})
	}
	return g, nil
}

// Close lets go of idle connections.
func (g *gateway) Close() error {
	g.client.CloseIdleConnections()
	return nil
}

// Test lists the gateway's SSL objects with the key, changing nothing.
func (g *gateway) Test(ctx context.Context) (contracts.Message, error) {
	var list struct {
		Total int `json:"total"`
	}
	if err := g.call(ctx, http.MethodGet, "ssls", nil, &list); err != nil {
		return contracts.Message{}, g.explain(err)
	}
	return contracts.Message{Key: i18n.ApisixTestOk, Params: map[string]any{"count": list.Total}}, nil
}

// objectFor is the ID of the SSL object of the certificate certificate:
// the one set, else one of JEC's own naming.
func (g *gateway) objectFor(certificate int64) string {
	if g.id != "" {
		return g.id
	}
	return "jec-" + strconv.FormatInt(certificate, 10)
}

// Deploy puts the certificate in its SSL object, made or replaced at once,
// for the certificate's names: the gateway serves it for them, so there
// is nothing to remove afterwards.
func (g *gateway) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("apisix: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	id := g.objectFor(req.CertificateID)
	names := cryptox.Names(req.Chain[0])
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.ApisixLogPut, Params: map[string]any{"id": id, "names": strings.Join(names, ", ")}})
	body := map[string]any{
		"cert": string(cryptox.EncodeCertificates(req.Chain...)), "key": string(key), "snis": names,
		"labels": map[string]string{"managed_by": "jec"},
	}
	if err := g.call(ctx, http.MethodPut, "ssls/"+id, body, nil); err != nil {
		return none, g.explain(err)
	}
	return none, nil
}

// apiError is an answer of the Admin API that says it failed.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("apisix: HTTP %d %s", e.status, e.message) }

// call sends body, if any, as JSON to path of the Admin API with the key,
// and decodes the answer into out.
func (g *gateway) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.base.JoinPath(path).String(), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-API-KEY", g.key)
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var answer struct {
			Message string `json:"error_msg"`
		}
		_ = json.Unmarshal(raw, &answer)
		return &apiError{status: resp.StatusCode, message: answer.Message}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a refused key, ends the attempts.
func (g *gateway) explain(err error) error {
	var ae *apiError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &ae) && (ae.status == http.StatusUnauthorized || ae.status == http.StatusForbidden):
		return retry.Permanent(&contracts.Problem{Key: i18n.ApisixErrorKey, Err: err})
	case errors.As(err, &ae) && ae.message == "":
		problem := &contracts.Problem{Key: i18n.ApisixErrorStatus, Params: map[string]any{"status": ae.status}, Err: err}
		if ae.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.ApisixErrorApi, Params: map[string]any{"message": ae.message}, Err: err}
		if ae.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.ApisixErrorPrivate, Params: map[string]any{"host": g.host}})
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.ApisixErrorPlain, Params: map[string]any{"host": g.host}})
	case errors.Is(err, httpx.ErrPin):
		return retry.Permanent(&contracts.Problem{Key: i18n.ApisixErrorPin, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.ApisixErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.ApisixErrorNetwork, Params: map[string]any{"host": g.host}, Err: httpx.Cause(err)}
}

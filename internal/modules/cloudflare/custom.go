package cloudflare

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
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// api is the address of Cloudflare's API.
const api = "https://api.cloudflare.com/client/v4"

func customType() providers.Type {
	return providers.Type{
		ID: "cloudflare.custom", Kind: providers.Target, Name: i18n.CloudflareCustomName, Description: i18n.CloudflareCustomDescription,
		Testable: true,
		Fields: []providers.Field{
			{Key: "api_token", Label: i18n.CloudflareFieldApiToken, Help: i18n.CloudflareFieldApiTokenHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "zone", Label: i18n.CloudflareFieldZone, Help: i18n.CloudflareFieldZoneHelp, Kind: providers.Text, Required: true},
			{Key: "certificate_id", Label: i18n.CloudflareFieldCertificateId, Help: i18n.CloudflareFieldCertificateIdHelp, Kind: providers.Text, Advanced: true},
		},
	}
}

// zoneID is the form of a zone's ID, as against its name.
var zoneID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// zone is a zone of Cloudflare, with its custom certificates.
type zone struct {
	client *http.Client
	base   string
	token  string
	// zone is the zone's ID, or its name until resolve looks the ID up.
	zone string
	// certificate is the custom certificate set to be updated; "" for one
	// of JEC's own.
	certificate string
}

var (
	_ contracts.Target = (*zone)(nil)
	_ contracts.Tester = (*zone)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	client, err := httpx.Client(httpx.Options{PublicOnly: true})
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &zone{
		client: client, base: api, token: strings.TrimSpace(in.Value("api_token")),
		zone:        strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Config["zone"]), ".")),
		certificate: strings.TrimSpace(in.Config["certificate_id"]),
	}, nil
}

// Close lets the connections go.
func (z *zone) Close() error {
	z.client.CloseIdleConnections()
	return nil
}

// apiError is an error the API answered.
type apiError struct {
	status  int
	code    int
	message string
	after   time.Duration // as Retry-After asks, for 429
}

func (e *apiError) Error() string {
	return fmt.Sprintf("cloudflare: HTTP %d: [%d] %s", e.status, e.code, e.message)
}

// envelope is what every answer of the API comes in.
type envelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		TotalCount int `json:"total_count"`
	} `json:"result_info"`
}

// call sends a request to path below the API, with body as JSON unless
// nil, and decodes the result into out unless nil. It returns the whole
// envelope too.
func (z *zone) call(ctx context.Context, method, path string, body, out any) (*envelope, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, retry.Permanent(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, z.base+path, r)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	req.Header.Set("Authorization", "Bearer "+z.token)
	req.Header.Set("User-Agent", "JEC/"+buildinfo.Get().Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := z.client.Do(req)
	if err != nil {
		return nil, explain(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env envelope
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, explain(err)
	}
	if json.Unmarshal(data, &env) != nil || !env.Success || resp.StatusCode >= 300 {
		e := &apiError{status: resp.StatusCode, message: http.StatusText(resp.StatusCode)}
		if len(env.Errors) > 0 {
			e.code, e.message = env.Errors[0].Code, env.Errors[0].Message
		}
		if d, ok := retry.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			e.after = d
		}
		return nil, e
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return nil, fmt.Errorf("cloudflare: %s %s: %w", method, path, err)
		}
	}
	return &env, nil
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a token Cloudflare refuses, ends the attempts.
func explain(err error) error {
	var e *apiError
	if !errors.As(err, &e) {
		if errors.Is(err, httpx.ErrNotPublic) {
			return retry.Permanent(&contracts.Problem{Key: i18n.CloudflareErrorNetwork, Err: err})
		}
		return &contracts.Problem{Key: i18n.CloudflareErrorNetwork, Err: httpx.Cause(err)}
	}
	switch {
	case e.status == http.StatusTooManyRequests:
		p := &contracts.Problem{Key: i18n.CloudflareErrorThrottled, Err: err}
		if e.after > 0 {
			return retry.After(p, e.after)
		}
		return p
	case e.status == http.StatusUnauthorized, e.status == http.StatusForbidden, e.code == 10000, e.code == 9109:
		return retry.Permanent(&contracts.Problem{Key: i18n.CloudflareErrorAuth, Params: map[string]any{"message": e.message}, Err: err})
	}
	p := &contracts.Problem{Key: i18n.CloudflareErrorApi, Params: map[string]any{"code": e.code, "message": e.message}, Err: err}
	if e.status >= 500 {
		return p
	}
	return retry.Permanent(p)
}

// resolve finds the zone's ID when it was given by name.
func (z *zone) resolve(ctx context.Context) error {
	if zoneID.MatchString(z.zone) {
		return nil
	}
	var zones []struct {
		ID string `json:"id"`
	}
	if _, err := z.call(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(z.zone), nil, &zones); err != nil {
		return explain(err)
	}
	if len(zones) == 0 {
		return retry.Permanent(&contracts.Problem{Key: i18n.CloudflareErrorZone, Params: map[string]any{"zone": z.zone}})
	}
	z.zone = zones[0].ID
	return nil
}

// Test finds the zone and counts its custom certificates, changing nothing.
func (z *zone) Test(ctx context.Context) (contracts.Message, error) {
	if err := z.resolve(ctx); err != nil {
		return contracts.Message{}, err
	}
	var info struct {
		Name string `json:"name"`
	}
	if _, err := z.call(ctx, http.MethodGet, "/zones/"+z.zone, nil, &info); err != nil {
		return contracts.Message{}, explain(err)
	}
	env, err := z.call(ctx, http.MethodGet, "/zones/"+z.zone+"/custom_certificates?per_page=5", nil, nil)
	if err != nil {
		return contracts.Message{}, explain(err)
	}
	return contracts.Message{Key: i18n.CloudflareTestOk, Params: map[string]any{"zone": info.Name, "count": env.ResultInfo.TotalCount}}, nil
}

// custom is a custom certificate as the API lists it.
type custom struct {
	ID        string   `json:"id"`
	Hosts     []string `json:"hosts"`
	ExpiresOn string   `json:"expires_on"`
}

// upload is what a custom certificate is made or replaced with: the chain
// and the key, bundled by Cloudflare for the most clients.
type upload struct {
	Certificate  string `json:"certificate"`
	PrivateKey   string `json:"private_key"`
	BundleMethod string `json:"bundle_method"`
	Type         string `json:"type,omitempty"`
}

// Deploy replaces the custom certificate set, or the one an earlier
// version went to, with the new certificate; the first time, or when that
// one is gone, it adds a custom certificate. Replacing in place keeps the
// zone serving a certificate throughout.
func (z *zone) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("cloudflare: no certificate to deploy"))
	}
	if err := z.resolve(ctx); err != nil {
		return none, err
	}
	key, err := cryptox.EncodeKeyTraditional(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	body := upload{Certificate: string(cryptox.EncodeCertificates(req.Chain...)), PrivateKey: string(key), BundleMethod: "ubiquitous"}
	target := z.certificate
	if target == "" && len(req.Previous) > 0 {
		target = req.Previous[0]
	}
	if target != "" {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.CloudflareLogReplace, Params: map[string]any{"id": target}})
		_, err := z.call(ctx, http.MethodPatch, "/zones/"+z.zone+"/custom_certificates/"+url.PathEscape(target), body, nil)
		var e *apiError
		switch {
		case err == nil:
			return contracts.DeployResult{Ref: target}, nil
		case !errors.As(err, &e) || e.status != http.StatusNotFound || z.certificate != "":
			return none, explain(err)
		}
		log(contracts.LogLine{Level: contracts.Warn, Key: i18n.CloudflareLogGone, Params: map[string]any{"id": target}})
	}
	id, err := z.find(ctx, req)
	if err != nil {
		return none, err
	}
	if id == "" {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.CloudflareLogAdd})
		body.Type = "sni_custom"
		var made custom
		if _, err := z.call(ctx, http.MethodPost, "/zones/"+z.zone+"/custom_certificates", body, &made); err != nil {
			return none, explain(err)
		}
		id = made.ID
	}
	// The one gone, and any other earlier ones, are replaced.
	var removed []string
	for _, p := range req.Previous {
		if p != id {
			removed = append(removed, p)
		}
	}
	return contracts.DeployResult{Ref: id, Removed: removed}, nil
}

// find returns the ID of the custom certificate an earlier attempt added
// for req, by its hosts and end, or "".
func (z *zone) find(ctx context.Context, req contracts.DeployRequest) (string, error) {
	leaf := req.Chain[0]
	hosts := slices.Clone(leaf.DNSNames)
	slices.Sort(hosts)
	for page := 1; page <= 20; page++ {
		var list []custom
		if _, err := z.call(ctx, http.MethodGet, fmt.Sprintf("/zones/%s/custom_certificates?per_page=50&page=%d", z.zone, page), nil, &list); err != nil {
			return "", explain(err)
		}
		for _, c := range list {
			got := slices.Clone(c.Hosts)
			slices.Sort(got)
			end, err := time.Parse(time.RFC3339, c.ExpiresOn)
			if err == nil && slices.Equal(got, hosts) && end.Equal(leaf.NotAfter.Truncate(time.Second)) {
				return c.ID, nil
			}
		}
		if len(list) < 50 {
			break
		}
	}
	return "", nil
}

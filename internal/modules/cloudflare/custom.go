package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

func customType() providers.Type {
	return providers.Type{
		ID: "cloudflare.custom", Kind: providers.Target, Name: i18n.CloudflareCustomName, Description: i18n.CloudflareCustomDescription, Untested: true,
		Testable: true,
		Fields: []providers.Field{
			credentials.CloudflareAuth,
			{Key: "api_token", Label: i18n.CloudflareFieldApiToken, Help: i18n.CloudflareFieldApiTokenHelp, Kind: providers.Text, Secret: true, Required: true, When: credentials.CloudflareToken},
			{Key: "api_email", Label: i18n.CloudflareFieldApiEmail, Help: i18n.CloudflareFieldApiEmailHelp, Kind: providers.Text, Required: true, When: credentials.CloudflareGlobal},
			{Key: "api_key", Label: i18n.CloudflareFieldApiKey, Help: i18n.CloudflareFieldApiKeyHelp, Kind: providers.Text, Secret: true, Required: true, When: credentials.CloudflareGlobal},
			{Key: "zone", Label: i18n.CloudflareFieldZone, Help: i18n.CloudflareFieldZoneHelp, Kind: providers.Text, Required: true},
			{Key: "certificate_id", Label: i18n.CloudflareFieldCertificateId, Help: i18n.CloudflareFieldCertificateIdHelp, Kind: providers.Text, Advanced: true},
			httpx.ProxyField(),
		},
		Credential: credentials.Use(credentials.Cloudflare),
	}
}

// zoneID is the form of a zone's ID, as against its name.
var zoneID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// zone is a zone of Cloudflare, with its custom certificates.
type zone struct {
	rest
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
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: true}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r, err := signIn(in, client, api)
	if err != nil {
		return nil, err
	}
	return &zone{
		rest:        r,
		zone:        strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Config["zone"]), ".")),
		certificate: strings.TrimSpace(in.Config["certificate_id"]),
	}, nil
}

// Close lets the connections go.
func (z *zone) Close() error {
	z.client.CloseIdleConnections()
	return nil
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

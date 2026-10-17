package esa

import (
	"context"
	"errors"
	"fmt"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	esaapi "github.com/go-acme/esa-20240910/v3/client"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// siteAPI is what the target needs of ESA's API (version 2024-09-10).
// Tests replace it with a fake.
type siteAPI interface {
	// siteID returns the ID of the site called name, or errSiteNotFound.
	siteID(ctx context.Context, name string) (int64, error)
	// certificates lists the certificates of a site.
	certificates(ctx context.Context, site int64) ([]certificate, error)
	// upload adds a certificate to a site and returns its ID.
	upload(ctx context.Context, site int64, name, chain, key string) (string, error)
	// remove deletes certificate id from a site.
	remove(ctx context.Context, site int64, id string) error
}

// certificate is a certificate of a site as ESA lists it.
type certificate struct {
	ID, Name string
	// Fingerprint is the SHA-256 of the certificate, in lowercase hex
	// without separators.
	Fingerprint string
}

var errSiteNotFound = errors.New("no such site")

// Endpoints of the two editions of ESA, by region.
const (
	regionChina  = "cn-hangzhou"
	regionGlobal = "ap-southeast-1"
)

// sdk is siteAPI over Alibaba Cloud's SDK.
type sdk struct{ client *esaapi.Client }

func newSDK(in providers.Instance) (siteAPI, error) {
	region := in.Config["region"]
	if region == "" {
		region = regionChina
	}
	cfg := new(openapi.Config).
		SetRegionId(region).
		SetAccessKeyId(in.Value("access_key_id")).
		SetAccessKeySecret(in.Value("access_key_secret")).
		SetConnectTimeout(10_000).
		SetReadTimeout(30_000)
	client, err := esaapi.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("esa: new client: %w", err)
	}
	// The SDK ignores the region unless the endpoint rule is regional, as
	// lego's ESA DNS provider found out.
	client.Endpoint = nil
	client.EndpointRule = dara.String("regional")
	client.Endpoint, err = esaapi.GetEndpoint(client, dara.String("esa"), client.RegionId, client.EndpointRule,
		client.Network, client.Suffix, client.EndpointMap, client.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("esa: endpoint: %w", err)
	}
	return &sdk{client: client}, nil
}

func (s *sdk) siteID(ctx context.Context, name string) (int64, error) {
	req := new(esaapi.ListSitesRequest).SetSiteName(name).SetSiteSearchType("exact").SetPageSize(50)
	resp, err := esaapi.ListSitesWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
	if err != nil {
		return 0, err
	}
	for _, site := range resp.Body.GetSites() {
		if strings.EqualFold(dara.StringValue(site.GetSiteName()), name) {
			return dara.Int64Value(site.GetSiteId()), nil
		}
	}
	return 0, errSiteNotFound
}

func (s *sdk) certificates(ctx context.Context, site int64) ([]certificate, error) {
	const size = 100
	var out []certificate
	for page := int64(1); ; page++ {
		req := new(esaapi.ListCertificatesRequest).SetSiteId(site).SetPageNumber(page).SetPageSize(size)
		resp, err := esaapi.ListCertificatesWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
		if err != nil {
			return nil, err
		}
		results := resp.Body.GetResult()
		for _, r := range results {
			out = append(out, certificate{
				ID: dara.StringValue(r.GetId()), Name: dara.StringValue(r.GetName()),
				Fingerprint: normalizeFingerprint(dara.StringValue(r.GetFingerprintSha256())),
			})
		}
		if len(results) < size || int64(len(out)) >= dara.Int64Value(resp.Body.GetTotalCount()) {
			return out, nil
		}
	}
}

func (s *sdk) upload(ctx context.Context, site int64, name, chain, key string) (string, error) {
	req := new(esaapi.SetCertificateRequest).SetSiteId(site).SetType("upload").SetName(name).SetCertificate(chain).SetPrivateKey(key)
	resp, err := esaapi.SetCertificateWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
	if err != nil {
		return "", err
	}
	id := dara.StringValue(resp.Body.GetId())
	if id == "" {
		return "", errors.New("esa: SetCertificate returned no certificate ID")
	}
	return id, nil
}

func (s *sdk) remove(ctx context.Context, site int64, id string) error {
	req := new(esaapi.DeleteCertificateRequest).SetSiteId(site).SetId(id)
	_, err := esaapi.DeleteCertificateWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
	return err
}

// normalizeFingerprint writes a SHA-256 fingerprint as lowercase hex
// without separators, as ESA may use colons or uppercase.
func normalizeFingerprint(f string) string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(f))
}

// apiErrorCode returns the code and HTTP status of an error of ESA's API,
// or "" and 0 for another error.
func apiErrorCode(err error) (string, int) {
	var e *dara.SDKError
	if errors.As(err, &e) {
		return dara.StringValue(e.Code), dara.IntValue(e.StatusCode)
	}
	return "", 0
}

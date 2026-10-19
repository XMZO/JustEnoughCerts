package huaweicloud

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	hwauth "github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/global"
	hwconfig "github.com/huaweicloud/huaweicloud-sdk-go-v3/core/config"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	hwcdn "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/model"
	hwregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/region"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// The sites of Huawei Cloud, by the region its CDN answers in.
const (
	siteChina  = "cn-north-1"
	siteIntl   = "ap-southeast-1"
	siteEurope = "eu-west-101"
)

// timeout bounds a call of the API, which the SDK cannot cancel.
const timeout = 30 * time.Second

func cdnType() providers.Type {
	return providers.Type{
		ID: "huaweicloud.cdn", Kind: providers.Target, Name: i18n.HuaweicloudCdnName, Description: i18n.HuaweicloudCdnDescription, Untested: true,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.HuaweicloudFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "secret_access_key", Label: i18n.HuaweicloudFieldSecretAccessKey, Help: i18n.HuaweicloudFieldSecretAccessKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "site", Label: i18n.HuaweicloudFieldSite, Kind: providers.Select, Required: true, Default: siteChina, Options: []providers.Option{
				{Value: siteChina, Label: i18n.HuaweicloudSiteChina},
				{Value: siteIntl, Label: i18n.HuaweicloudSiteIntl},
				{Value: siteEurope, Label: i18n.HuaweicloudSiteEurope},
			}},
			{Key: "domains", Label: i18n.HuaweicloudFieldDomains, Help: i18n.HuaweicloudFieldDomainsHelp, Kind: providers.Text},
			{Key: "domain_id", Label: i18n.HuaweicloudFieldDomainId, Help: i18n.HuaweicloudFieldDomainIdHelp, Kind: providers.Text, Advanced: true},
		},
		Credential: credentials.Use(credentials.HuaweiCloud),
	}
}

// cdn is the CDN of an account. It keeps the two calls it makes, not the
// SDK's client: a value held where an interface can reach it keeps all its
// methods in the program, one for each API of the CDN.
type cdn struct {
	show    func(*model.ShowDomainFullConfigRequest) (*model.ShowDomainFullConfigResponse, error)
	update  func(*model.UpdateDomainFullConfigRequest) (*model.UpdateDomainFullConfigResponse, error)
	domains []string
}

var (
	_ contracts.Target = (*cdn)(nil)
	_ contracts.Tester = (*cdn)(nil)
)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	auth := hwauth.NewCredentialsBuilder().
		WithAk(strings.TrimSpace(in.Value("access_key_id"))).WithSk(strings.TrimSpace(in.Value("secret_access_key")))
	if id := strings.TrimSpace(in.Config["domain_id"]); id != "" {
		auth = auth.WithDomainId(id)
	}
	cred, err := auth.SafeBuild()
	if err != nil {
		return nil, retry.Permanent(err)
	}
	builder := hwcdn.CdnClientBuilder().WithCredential(cred).
		WithHttpConfig(hwconfig.DefaultHttpConfig().WithTimeout(timeout).WithRetries(0))
	if m.endpoint != "" {
		builder = builder.WithEndpoints([]string{m.endpoint})
	} else {
		region, err := hwregion.SafeValueOf(in.Config["site"])
		if err != nil {
			return nil, retry.Permanent(err)
		}
		builder = builder.WithRegion(region)
	}
	hc, err := builder.SafeBuild()
	if err != nil {
		// The SDK looks the account up as it builds the client.
		return nil, explain(err, "")
	}
	client := hwcdn.NewCdnClient(hc)
	return &cdn{show: client.ShowDomainFullConfig, update: client.UpdateDomainFullConfig, domains: split(in.Config["domains"])}, nil
}

// split reads a list of domains, separated by commas or spaces.
func split(s string) []string {
	var out []string
	for _, d := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		out = append(out, strings.ToLower(strings.TrimSuffix(d, ".")))
	}
	return out
}

// Close has nothing to release.
func (*cdn) Close() error { return nil }

// https returns the HTTPS settings of domain.
func (c *cdn) https(domain string) (*model.HttpGetBody, error) {
	res, err := c.show(&model.ShowDomainFullConfigRequest{DomainName: domain})
	if err != nil {
		return nil, explain(err, domain)
	}
	if res.Configs == nil {
		return nil, nil
	}
	return res.Configs.Https, nil
}

// Test reads the settings of each accelerated domain, changing nothing.
func (c *cdn) Test(context.Context) (contracts.Message, error) {
	if len(c.domains) == 0 {
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.HuaweicloudErrorNoDomains})
	}
	for _, d := range c.domains {
		if _, err := c.https(d); err != nil {
			return contracts.Message{}, err
		}
	}
	return contracts.Message{Key: i18n.HuaweicloudTestOk, Params: map[string]any{"domains": strings.Join(c.domains, ", ")}}, nil
}

// Deploy sets the certificate on each accelerated domain: those set, or
// else the certificate's own, but for wildcards. A domain keeps its HTTP/2,
// TLS versions and OCSP stapling, which an update without them would
// reset.
func (c *cdn) Deploy(_ context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("huaweicloud: no certificate to deploy"))
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
		return none, retry.Permanent(&contracts.Problem{Key: i18n.HuaweicloudErrorNoDomains})
	}
	key, err := cryptox.EncodeKeyTraditional(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	name := "jec-" + strconv.FormatInt(req.CertificateID, 10) + "-" + cryptox.Fingerprint(req.Chain[0])[:12]
	chain := string(cryptox.EncodeCertificates(req.Chain...))
	for _, d := range domains {
		was, err := c.https(d)
		if err != nil {
			return none, err
		}
		body := &model.HttpPutBody{
			HttpsStatus: ptr("on"), CertificateType: ptr("server"), CertificateSource: ptr(int32(0)),
			CertificateName: ptr(name), CertificateValue: ptr(chain), PrivateKey: ptr(string(key)),
		}
		if was != nil {
			body.Http2Status, body.TlsVersion, body.OcspStaplingStatus = was.Http2Status, was.TlsVersion, was.OcspStaplingStatus
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.HuaweicloudLogSet, Params: map[string]any{"domain": d}})
		_, err = c.update(&model.UpdateDomainFullConfigRequest{
			DomainName: d, Body: &model.ModifyDomainConfigRequestBody{Configs: &model.Configs{Https: body}},
		})
		if err != nil {
			return none, explain(err, d)
		}
	}
	return none, nil
}

func ptr[T any](v T) *T { return &v }

// explain turns an error of the API about domain into a Problem: one that
// trying again cannot fix, such as a key the API refuses, ends the
// attempts.
func explain(err error, domain string) error {
	var e *sdkerr.ServiceResponseError
	if !errors.As(err, &e) {
		return &contracts.Problem{Key: i18n.HuaweicloudErrorNetwork, Err: err}
	}
	switch {
	case e.StatusCode == http.StatusTooManyRequests:
		return &contracts.Problem{Key: i18n.HuaweicloudErrorThrottled, Err: err}
	case e.StatusCode == http.StatusUnauthorized, e.StatusCode == http.StatusForbidden, e.ErrorCode == "APIGW.0301":
		return retry.Permanent(&contracts.Problem{Key: i18n.HuaweicloudErrorAuth, Params: map[string]any{"code": e.ErrorCode}, Err: err})
	case e.StatusCode == http.StatusNotFound && domain != "":
		return retry.Permanent(&contracts.Problem{Key: i18n.HuaweicloudErrorDomain, Params: map[string]any{"domain": domain}, Err: err})
	}
	p := &contracts.Problem{Key: i18n.HuaweicloudErrorApi, Params: map[string]any{"code": e.ErrorCode, "message": e.ErrorMessage}, Err: err}
	if e.StatusCode >= 500 {
		return p
	}
	return retry.Permanent(p)
}

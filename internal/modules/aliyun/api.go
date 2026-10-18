package aliyun

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/aliyunapi"
)

// service is CDN or DCDN, whose APIs are alike.
type service struct {
	id       string
	name     i18n.Key
	endpoint string
	version  string
	// set installs a certificate on a domain; describe reads it.
	set      string
	describe string
}

var (
	cdn = service{
		id: "cdn", name: i18n.AliyunCdnName, endpoint: "cdn.aliyuncs.com", version: "2018-05-10",
		set: "SetCdnDomainSSLCertificate", describe: "DescribeDomainCertificateInfo",
	}
	dcdn = service{
		id: "dcdn", name: i18n.AliyunDcdnName, endpoint: "dcdn.aliyuncs.com", version: "2018-01-15",
		set: "SetDcdnDomainSSLCertificate", describe: "DescribeDcdnDomainCertificateInfo",
	}
)

// cdnAPI is what a target needs of the API of its service. Tests replace
// it with a fake.
type cdnAPI interface {
	// setCertificate uploads the certificate, in PEM with its chain, and
	// its key, and serves it on domain, with HTTPS on.
	setCertificate(ctx context.Context, domain, chain, key string) error
	// check makes sure domain is an accelerated domain of the account.
	check(ctx context.Context, domain string) error
}

// sdk is cdnAPI over Alibaba Cloud's OpenAPI client, which signs the
// requests, without the SDKs of the services.
type sdk struct {
	client   *openapi.Client
	svc      service
	protocol string
}

func newSDK(svc service, id, secret string) (cdnAPI, error) {
	return dial(svc, id, secret, svc.endpoint, "HTTPS")
}

// dial makes the client of svc at endpoint, over protocol.
func dial(svc service, id, secret, endpoint, protocol string) (*sdk, error) {
	cfg := new(openapiutil.Config).
		SetAccessKeyId(id).SetAccessKeySecret(secret).
		SetEndpoint(endpoint).SetProtocol(protocol).
		SetConnectTimeout(10_000).SetReadTimeout(30_000)
	client, err := openapi.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("aliyun: new client: %w", err)
	}
	return &sdk{client: client, svc: svc, protocol: protocol}, nil
}

// call sends an RPC request, with query in the URL and form in the body,
// as the SDKs of Alibaba Cloud do.
func (s *sdk) call(ctx context.Context, action string, query, form map[string]string) (map[string]any, error) {
	q := map[string]*string{}
	for k, v := range query {
		q[k] = dara.String(v)
	}
	req := &openapiutil.OpenApiRequest{Query: q}
	if len(form) > 0 {
		body := map[string]any{}
		for k, v := range form {
			body[k] = v
		}
		req.Body = openapiutil.ParseToMap(body)
	}
	params := &openapiutil.Params{
		Action: dara.String(action), Version: dara.String(s.svc.version), Protocol: dara.String(s.protocol),
		Pathname: dara.String("/"), Method: dara.String("POST"), AuthType: dara.String("AK"),
		Style: dara.String("RPC"), ReqBodyType: dara.String("formData"), BodyType: dara.String("json"),
	}
	return s.client.CallApiWithCtx(ctx, params, req, &dara.RuntimeOptions{})
}

func (s *sdk) setCertificate(ctx context.Context, domain, chain, key string) error {
	_, err := s.call(ctx, s.svc.set,
		map[string]string{"DomainName": domain, "SSLProtocol": "on", "CertType": "upload"},
		map[string]string{"SSLPub": chain, "SSLPri": key})
	return err
}

func (s *sdk) check(ctx context.Context, domain string) error {
	_, err := s.call(ctx, s.svc.describe, map[string]string{"DomainName": domain}, nil)
	return err
}

// explain turns an error of the API about domain into a Problem: one that
// trying again cannot fix, such as a domain the account does not have,
// ends the attempts.
func explain(err error, svc service, domain string) error {
	code, status := aliyunapi.ErrorCode(err)
	lower := strings.ToLower(code)
	switch {
	case code == "":
		return &contracts.Problem{Key: i18n.AliyunErrorNetwork, Params: map[string]any{"endpoint": svc.endpoint}, Err: err}
	case status == http.StatusUnauthorized, strings.HasPrefix(code, "InvalidAccessKeyId"), code == "SignatureDoesNotMatch",
		strings.Contains(lower, "forbidden"), strings.Contains(lower, "nopermission"), status == http.StatusForbidden:
		return retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorAuth, Err: err})
	case strings.Contains(lower, "domain") && (strings.Contains(lower, "notfound") || strings.Contains(lower, "notexist") || strings.Contains(lower, "notbelong")):
		return retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorDomain, Params: map[string]any{"domain": domain}, Err: err})
	case strings.Contains(lower, "throttling"), status == http.StatusTooManyRequests:
		return &contracts.Problem{Key: i18n.AliyunErrorThrottled, Err: err}
	}
	p := &contracts.Problem{Key: i18n.AliyunErrorApi, Params: map[string]any{"code": code}, Err: err}
	if status >= 500 {
		return p
	}
	return retry.Permanent(p)
}

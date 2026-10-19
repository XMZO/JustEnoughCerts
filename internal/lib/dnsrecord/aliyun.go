package dnsrecord

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
)

// aliyun is Alibaba Cloud DNS (alidns 2015-01-09), through the OpenAPI
// client, which signs the requests, without the SDK of the service.
type aliyun struct {
	client   *openapi.Client
	protocol string
	ttl      int
}

// aliyunEndpoint is the API's address; tests replace it, with the
// protocol.
var aliyunEndpoint, aliyunProtocol = "alidns.aliyuncs.com", "HTTPS"

func openAliyun(settings map[string]string) (API, error) {
	id, secret := first(settings, "ALICLOUD_ACCESS_KEY"), first(settings, "ALICLOUD_SECRET_KEY")
	if id == "" || secret == "" {
		return nil, ErrUnsupported // an ECS role, say: lego's alone
	}
	cfg := new(openapiutil.Config).SetAccessKeyId(id).SetAccessKeySecret(secret).
		SetEndpoint(aliyunEndpoint).SetProtocol(aliyunProtocol).SetConnectTimeout(10_000).SetReadTimeout(30_000)
	if token := first(settings, "ALICLOUD_SECURITY_TOKEN"); token != "" {
		cfg.SetSecurityToken(token)
	}
	client, err := openapi.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("dnsrecord: alidns client: %w", err)
	}
	return &aliyun{client: client, protocol: aliyunProtocol, ttl: ttl(settings, "ALICLOUD_TTL", 600)}, nil
}

// call sends an RPC request with params and decodes the answer into out.
func (a *aliyun) call(ctx context.Context, action string, params map[string]string, out any) error {
	q := map[string]*string{}
	for k, v := range params {
		q[k] = dara.String(v)
	}
	res, err := a.client.CallApiWithCtx(ctx, &openapiutil.Params{
		Action: dara.String(action), Version: dara.String("2015-01-09"), Protocol: dara.String(a.protocol),
		Pathname: dara.String("/"), Method: dara.String("POST"), AuthType: dara.String("AK"),
		Style: dara.String("RPC"), ReqBodyType: dara.String("formData"), BodyType: dara.String("json"),
	}, &openapiutil.OpenApiRequest{Query: q}, &dara.RuntimeOptions{})
	if err != nil {
		return fmt.Errorf("dnsrecord: alidns %s: %w", action, err)
	}
	if out == nil {
		return nil
	}
	b, err := json.Marshal(res["body"])
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (a *aliyun) Add(ctx context.Context, r Record) (string, error) {
	if err := valid(r); err != nil {
		return "", err
	}
	zone, sub, err := split(ctx, r.Name)
	if err != nil {
		return "", err
	}
	var out struct {
		RecordID string `json:"RecordId"`
	}
	err = a.call(ctx, "AddDomainRecord", map[string]string{
		"DomainName": zone, "RR": sub, "Type": r.Type, "Value": r.Value, "TTL": strconv.Itoa(a.ttl),
	}, &out)
	if err != nil {
		return "", err
	}
	if out.RecordID == "" {
		return "", fmt.Errorf("dnsrecord: alidns AddDomainRecord answered no RecordId")
	}
	return out.RecordID, nil
}

func (a *aliyun) Domains(ctx context.Context) ([]string, int, error) {
	var out struct {
		TotalCount int `json:"TotalCount"`
		Domains    struct {
			Domain []struct {
				DomainName string `json:"DomainName"`
			} `json:"Domain"`
		} `json:"Domains"`
	}
	if err := a.call(ctx, "DescribeDomains", map[string]string{"PageSize": strconv.Itoa(listed)}, &out); err != nil {
		return nil, 0, err
	}
	names := make([]string, 0, len(out.Domains.Domain))
	for _, d := range out.Domains.Domain {
		names = append(names, d.DomainName)
	}
	return names, max(out.TotalCount, len(names)), nil
}

// Holds looks zone up among the domains of the account.
func (a *aliyun) Holds(ctx context.Context, zone string) (bool, error) {
	var out struct {
		Domains struct {
			Domain []struct {
				DomainName string `json:"DomainName"`
			} `json:"Domain"`
		} `json:"Domains"`
	}
	if err := a.call(ctx, "DescribeDomains", map[string]string{"KeyWord": zone, "SearchMode": "EXACT", "PageSize": "10"}, &out); err != nil {
		return false, err
	}
	for _, d := range out.Domains.Domain {
		if strings.EqualFold(d.DomainName, zone) {
			return true, nil
		}
	}
	return false, nil
}

func (a *aliyun) Remove(ctx context.Context, _ Record, id string) error {
	return a.call(ctx, "DeleteDomainRecord", map[string]string{"RecordId": id}, nil)
}

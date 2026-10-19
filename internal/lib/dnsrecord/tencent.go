package dnsrecord

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tchttp "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/http"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// tencent is DNSPod, through Tencent Cloud's API (dnspod 2021-03-23) and
// the SDK's common client, which signs the requests (TC3-HMAC-SHA256).
type tencent struct {
	client *common.Client
	ttl    int
}

// lineDefault is the line every visitor resolves: the Chinese word for
// default, as the API names it.
const lineDefault = "\u9ed8\u8ba4"

// tencentEndpoint is the API's address; tests replace it, with the scheme.
var tencentEndpoint, tencentScheme = "dnspod.tencentcloudapi.com", "HTTPS"

func openTencent(settings map[string]string) (API, error) {
	id, key := first(settings, "TENCENTCLOUD_SECRET_ID"), first(settings, "TENCENTCLOUD_SECRET_KEY")
	if id == "" || key == "" {
		return nil, ErrUnsupported
	}
	p := profile.NewClientProfile()
	p.HttpProfile.Endpoint, p.HttpProfile.Scheme, p.HttpProfile.ReqTimeout = tencentEndpoint, tencentScheme, 30
	cred := common.NewCredential(id, key)
	if token := first(settings, "TENCENTCLOUD_SESSION_TOKEN"); token != "" {
		cred = common.NewTokenCredential(id, key, token)
	}
	// DNSPod's free plan has no TTL below 600 seconds.
	return &tencent{client: common.NewCommonClient(cred, first(settings, "TENCENTCLOUD_REGION"), p), ttl: ttl(settings, "TENCENTCLOUD_TTL", 600)}, nil
}

// call sends action with params and decodes the response into out.
func (t *tencent) call(ctx context.Context, action string, params map[string]any, out any) error {
	req := tchttp.NewCommonRequest("dnspod", "2021-03-23", action)
	req.SetContext(ctx)
	if err := req.SetActionParameters(params); err != nil {
		return err
	}
	resp := tchttp.NewCommonResponse()
	if err := t.client.Send(req, resp); err != nil {
		return fmt.Errorf("dnsrecord: DNSPod %s: %w", action, err)
	}
	var body struct {
		Response json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(resp.GetBody(), &body); err != nil {
		return fmt.Errorf("dnsrecord: DNSPod %s: %w", action, err)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body.Response, out)
}

func (t *tencent) Add(ctx context.Context, r Record) (string, error) {
	if err := valid(r); err != nil {
		return "", err
	}
	zone, sub, err := split(ctx, r.Name)
	if err != nil {
		return "", err
	}
	var out struct {
		RecordID json.Number `json:"RecordId"`
	}
	err = t.call(ctx, "CreateRecord", map[string]any{
		"Domain": zone, "SubDomain": sub, "RecordType": r.Type, "RecordLine": lineDefault, "Value": r.Value, "TTL": t.ttl,
	}, &out)
	if err != nil {
		return "", err
	}
	return zone + "/" + out.RecordID.String(), nil
}

func (t *tencent) Remove(ctx context.Context, _ Record, id string) error {
	zone, record, ok := cutLast(id, "/")
	if !ok {
		return fmt.Errorf("dnsrecord: no DNSPod record %q", id)
	}
	n, err := strconv.ParseUint(record, 10, 64)
	if err != nil {
		return fmt.Errorf("dnsrecord: no DNSPod record %q", id)
	}
	return t.call(ctx, "DeleteRecord", map[string]any{"Domain": zone, "RecordId": n}, nil)
}

func (t *tencent) Domains(ctx context.Context) ([]string, int, error) {
	var out struct {
		DomainCountInfo struct {
			AllTotal int `json:"AllTotal"`
		} `json:"DomainCountInfo"`
		DomainList []struct {
			Name string `json:"Name"`
		} `json:"DomainList"`
	}
	if err := t.call(ctx, "DescribeDomainList", map[string]any{"Limit": listed}, &out); err != nil {
		return nil, 0, err
	}
	names := make([]string, 0, len(out.DomainList))
	for _, d := range out.DomainList {
		names = append(names, d.Name)
	}
	return names, max(out.DomainCountInfo.AllTotal, len(names)), nil
}

// Holds looks zone up among the domains of the account.
func (t *tencent) Holds(ctx context.Context, zone string) (bool, error) {
	var out struct {
		DomainList []struct {
			Name string `json:"Name"`
		} `json:"DomainList"`
	}
	err := t.call(ctx, "DescribeDomainList", map[string]any{"Keyword": zone, "Limit": 100}, &out)
	// DNSPod answers an error, not an empty list, when nothing matches.
	if err != nil && strings.Contains(err.Error(), "ResourceNotFound") {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, d := range out.DomainList {
		if strings.EqualFold(d.Name, zone) {
			return true, nil
		}
	}
	return false, nil
}

// cutLast splits s around the last sep.
func cutLast(s, sep string) (before, after string, ok bool) {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}

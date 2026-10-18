package dnsrecord

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	hwbasic "github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	hwconfig "github.com/huaweicloud/huaweicloud-sdk-go-v3/core/config"
	hwdns "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2/model"
	hwregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2/region"
)

// huawei is Huawei Cloud's DNS, through its official SDK, which lego
// brings along. It keeps the three calls it makes, not the SDK's client,
// which would keep all its methods in the program.
type huawei struct {
	zones  func(*model.ListPublicZonesRequest) (*model.ListPublicZonesResponse, error)
	create func(*model.CreateRecordSetRequest) (*model.CreateRecordSetResponse, error)
	remove func(*model.DeleteRecordSetRequest) (*model.DeleteRecordSetResponse, error)
	ttl    int32
}

// huaweiEndpoint and huaweiProject replace the region's address of the
// API and the project, as tests do: with no project, the SDK asks IAM for
// the one of the region.
var huaweiEndpoint, huaweiProject string

func openHuawei(settings map[string]string) (API, error) {
	ak, sk := first(settings, "HUAWEICLOUD_ACCESS_KEY_ID"), first(settings, "HUAWEICLOUD_SECRET_ACCESS_KEY")
	region := first(settings, "HUAWEICLOUD_REGION")
	if ak == "" || sk == "" || region == "" {
		return nil, ErrUnsupported
	}
	auth := hwbasic.NewCredentialsBuilder().WithAk(ak).WithSk(sk)
	if huaweiProject != "" {
		auth = auth.WithProjectId(huaweiProject)
	}
	cred, err := auth.SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("dnsrecord: Huawei Cloud credentials: %w", err)
	}
	builder := hwdns.DnsClientBuilder().WithCredential(cred).
		WithHttpConfig(hwconfig.DefaultHttpConfig().WithTimeout(30 * time.Second).WithRetries(0))
	if huaweiEndpoint != "" {
		builder = builder.WithEndpoints([]string{huaweiEndpoint})
	} else {
		r, err := hwregion.SafeValueOf(region)
		if err != nil {
			return nil, fmt.Errorf("dnsrecord: Huawei Cloud region %s: %w", region, err)
		}
		builder = builder.WithRegion(r)
	}
	hc, err := builder.SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("dnsrecord: Huawei Cloud client: %w", err)
	}
	c := hwdns.NewDnsClient(hc)
	seconds := int32(ttl(settings, "HUAWEICLOUD_TTL", 300)) //nolint:gosec // G115: a day at most
	return &huawei{zones: c.ListPublicZones, create: c.CreateRecordSet, remove: c.DeleteRecordSet, ttl: seconds}, nil
}

func (h *huawei) Add(ctx context.Context, r Record) (string, error) {
	if err := valid(r); err != nil {
		return "", err
	}
	zone, _, err := split(ctx, r.Name)
	if err != nil {
		return "", err
	}
	res, err := h.zones(&model.ListPublicZonesRequest{Name: &zone})
	if err != nil {
		return "", fmt.Errorf("dnsrecord: Huawei Cloud zones: %w", err)
	}
	id := ""
	if res.Zones != nil {
		for _, z := range *res.Zones {
			if z.Name != nil && z.Id != nil && strings.TrimSuffix(*z.Name, ".") == zone {
				id = *z.Id
			}
		}
	}
	if id == "" {
		return "", fmt.Errorf("dnsrecord: Huawei Cloud has no zone %s", zone)
	}
	value := r.Value
	if r.Type == "TXT" {
		value = strconv.Quote(value) // Huawei Cloud keeps TXT values quoted
	}
	made, err := h.create(&model.CreateRecordSetRequest{ZoneId: id, Body: &model.CreateRecordSetRequestBody{
		Name: r.Name + ".", Type: r.Type, Ttl: &h.ttl, Records: []string{value},
	}})
	if err != nil {
		return "", fmt.Errorf("dnsrecord: Huawei Cloud record: %w", err)
	}
	if made.Id == nil {
		return "", fmt.Errorf("dnsrecord: Huawei Cloud answered no record set ID")
	}
	return id + "/" + *made.Id, nil
}

func (h *huawei) Remove(_ context.Context, _ Record, id string) error {
	zone, set, ok := strings.Cut(id, "/")
	if !ok {
		return fmt.Errorf("dnsrecord: no Huawei Cloud record %q", id)
	}
	if _, err := h.remove(&model.DeleteRecordSetRequest{ZoneId: zone, RecordsetId: set}); err != nil {
		return fmt.Errorf("dnsrecord: Huawei Cloud record: %w", err)
	}
	return nil
}

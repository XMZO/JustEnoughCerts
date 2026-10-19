package tencentcloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// The targets that deploy straight to resources, without a certificate
// there before to follow: CDN's domains, through SSL Certificates'
// deployment, and EdgeOne's hosts, through EdgeOne's API. Each version is
// uploaded to SSL Certificates first, as for tencentcloud.ssl.

// zoneID is the form of an EdgeOne zone's ID.
var zoneID = regexp.MustCompile(`^zone-[a-z0-9]+$`)

func directFields(list i18n.Key, listHelp i18n.Key, extra ...providers.Field) []providers.Field {
	fields := []providers.Field{
		{Key: "secret_id", Label: i18n.TencentcloudFieldSecretId, Kind: providers.Text, Required: true},
		{Key: "secret_key", Label: i18n.TencentcloudFieldSecretKey, Help: i18n.TencentcloudFieldDirectKeyHelp, Kind: providers.Text, Secret: true, Required: true},
	}
	fields = append(fields, extra...)
	return append(fields,
		providers.Field{Key: "hosts", Label: list, Help: listHelp, Kind: providers.Text, Required: true},
		providers.Field{Key: "delete_old", Label: i18n.TencentcloudFieldDeleteOld, Help: i18n.TencentcloudFieldDeleteOldHelp, Kind: providers.Switch, Default: "true"},
		providers.Field{Key: "site", Label: i18n.TencentcloudFieldSite, Kind: providers.Select, Default: siteChina, Advanced: true, Options: []providers.Option{
			{Value: siteChina, Label: i18n.TencentcloudSiteChina},
			{Value: siteIntl, Label: i18n.TencentcloudSiteInternational},
		}},
	)
}

func cdnType() providers.Type {
	return providers.Type{
		ID: "tencentcloud.cdn", Kind: providers.Target, Name: i18n.TencentcloudCdnName, Description: i18n.TencentcloudCdnDescription,
		Tags: []string{"china"}, Testable: true,
		Fields:     directFields(i18n.TencentcloudFieldCdnDomains, i18n.TencentcloudFieldCdnDomainsHelp),
		Credential: credentials.Use(credentials.TencentCloud),
	}
}

func teoType() providers.Type {
	return providers.Type{
		ID: "tencentcloud.teo", Kind: providers.Target, Name: i18n.TencentcloudTeoName, Description: i18n.TencentcloudTeoDescription,
		Tags: []string{"china"}, Testable: true,
		Fields: directFields(i18n.TencentcloudFieldTeoHosts, i18n.TencentcloudFieldTeoHostsHelp,
			providers.Field{Key: "zone", Label: i18n.TencentcloudFieldZone, Help: i18n.TencentcloudFieldZoneHelp, Kind: providers.Text, Required: true}),
		Credential: credentials.Use(credentials.TencentCloud),
	}
}

// direct deploys certificates straight to the hosts of a resource.
type direct struct {
	api       sslAPI
	endpoint  string
	resource  string // cdn or teo
	zone      string // EdgeOne's
	hosts     []string
	deleteOld bool
}

var (
	_ contracts.Target = (*direct)(nil)
	_ contracts.Tester = (*direct)(nil)
)

func (m *Module) directFactory(resource string) providers.Factory {
	return func(_ context.Context, in providers.Instance) (any, error) {
		endpoint := endpointChina
		if in.Config["site"] == siteIntl {
			endpoint = endpointIntl
		}
		d := &direct{
			endpoint: endpoint, resource: resource, zone: strings.TrimSpace(in.Config["zone"]),
			hosts: split(in.Config["hosts"]), deleteOld: in.Config["delete_old"] != "false",
		}
		switch {
		case len(d.hosts) == 0:
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorHosts})
		case resource == "teo" && !zoneID.MatchString(d.zone):
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorZone, Params: map[string]any{"zone": d.zone}})
		}
		api, err := m.open(strings.TrimSpace(in.Value("secret_id")), strings.TrimSpace(in.Value("secret_key")), endpoint)
		if err != nil {
			return nil, retry.Permanent(err)
		}
		d.api = api
		return d, nil
	}
}

// Close has nothing to release.
func (*direct) Close() error { return nil }

// Test makes sure the key works, without changing anything.
func (d *direct) Test(ctx context.Context) (contracts.Message, error) {
	n, err := d.api.count(ctx)
	if err != nil {
		return contracts.Message{}, explain(err, d.endpoint)
	}
	return contracts.Message{Key: i18n.TencentcloudTestOk, Params: map[string]any{"count": n}}, nil
}

// Deploy uploads the version, has the hosts take it, then deletes the
// certificates JEC uploaded before the one they had, if it is set to: that
// one goes the next time, once Tencent Cloud is done with it.
func (d *direct) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("tencentcloud: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	fp := sha256.Sum256(req.Chain[0].Raw)
	alias := "jec-" + strconv.FormatInt(req.CertificateID, 10) + "-" + hex.EncodeToString(fp[:6])
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.TencentcloudLogUpload, Params: map[string]any{"alias": alias}})
	id, err := d.api.upload(ctx, alias, string(cryptox.EncodeCertificates(req.Chain...)), string(key))
	if err != nil {
		return none, explain(err, d.endpoint)
	}
	log(contracts.LogLine{Level: contracts.OK, Key: i18n.TencentcloudLogUploaded, Params: map[string]any{"id": id}})

	hosts := strings.Join(d.hosts, ", ")
	if d.resource == "teo" {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.TencentcloudLogBindHosts, Params: map[string]any{"zone": d.zone, "hosts": hosts}})
		if err := d.api.bindHosts(ctx, d.zone, d.hosts, id); err != nil {
			return none, explain(err, d.endpoint)
		}
	} else {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.TencentcloudLogDeployDomains, Params: map[string]any{"domains": hosts}})
		record, err := d.api.deploy(ctx, id, d.resource, d.hosts)
		if err != nil {
			return none, explain(err, d.endpoint)
		}
		log(contracts.LogLine{Level: contracts.OK, Key: i18n.TencentcloudLogUpdating, Params: map[string]any{"record": record}})
	}

	var removed []string
	if d.deleteOld {
		for i, ref := range req.Previous {
			if ref == id || i == 0 {
				continue
			}
			if err := d.api.remove(ctx, ref); err != nil {
				log(contracts.LogLine{Level: contracts.Warn, Key: i18n.TencentcloudLogDeleteFailed, Params: map[string]any{"id": ref}, Detail: err.Error()})
				continue
			}
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.TencentcloudLogDeleted, Params: map[string]any{"id": ref}})
			removed = append(removed, ref)
		}
	}
	return contracts.DeployResult{Ref: id, Removed: removed}, nil
}

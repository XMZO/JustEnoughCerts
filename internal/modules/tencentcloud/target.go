package tencentcloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// Sites of Tencent Cloud.
const (
	siteChina = "china"
	siteIntl  = "international"
)

// How often, and how many times, JEC asks again while the update task is
// being made; tests shorten them.
var (
	updatePoll  = 3 * time.Second
	updateTries = 40
)

func sslType() providers.Type {
	return providers.Type{
		ID: "tencentcloud.ssl", Kind: providers.Target, Name: i18n.TencentcloudSslName, Description: i18n.TencentcloudSslDescription,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "secret_id", Label: i18n.TencentcloudFieldSecretId, Kind: providers.Text, Required: true},
			{Key: "secret_key", Label: i18n.TencentcloudFieldSecretKey, Help: i18n.TencentcloudFieldSecretKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "resources", Label: i18n.TencentcloudFieldResources, Help: i18n.TencentcloudFieldResourcesHelp, Kind: providers.Text, Required: true, Default: "cdn"},
			{Key: "replace", Label: i18n.TencentcloudFieldReplace, Help: i18n.TencentcloudFieldReplaceHelp, Kind: providers.Text},
			{Key: "delete_old", Label: i18n.TencentcloudFieldDeleteOld, Help: i18n.TencentcloudFieldDeleteOldHelp, Kind: providers.Switch, Default: "true"},
			{Key: "regions", Label: i18n.TencentcloudFieldRegions, Help: i18n.TencentcloudFieldRegionsHelp, Kind: providers.Text, Advanced: true},
			{Key: "site", Label: i18n.TencentcloudFieldSite, Kind: providers.Select, Default: siteChina, Advanced: true, Options: []providers.Option{
				{Value: siteChina, Label: i18n.TencentcloudSiteChina},
				{Value: siteIntl, Label: i18n.TencentcloudSiteInternational},
			}},
		},
	}
}

// target is the SSL Certificates service of an account.
type target struct {
	api       sslAPI
	endpoint  string
	types     []string
	regions   []string
	replace   string
	deleteOld bool
}

var (
	_ contracts.Target = (*target)(nil)
	_ contracts.Tester = (*target)(nil)
)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	endpoint := endpointChina
	if in.Config["site"] == siteIntl {
		endpoint = endpointIntl
	}
	api, err := m.open(strings.TrimSpace(in.Value("secret_id")), strings.TrimSpace(in.Value("secret_key")), endpoint)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	return &target{
		api: api, endpoint: endpoint,
		types: split(in.Config["resources"]), regions: split(in.Config["regions"]),
		replace: strings.TrimSpace(in.Config["replace"]), deleteOld: in.Config["delete_old"] != "false",
	}, nil
}

// split reads a list separated by commas or spaces, in lowercase.
func split(s string) []string {
	var out []string
	for _, v := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		out = append(out, strings.ToLower(v))
	}
	return out
}

// Close has nothing to release.
func (*target) Close() error { return nil }

// check refuses types of resource the service does not update.
func (t *target) check() error {
	if len(t.types) == 0 {
		return retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorResources})
	}
	for _, r := range t.types {
		if !slices.Contains(resourceTypes, r) {
			return retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorResource, Params: map[string]any{"resource": r, "types": strings.Join(resourceTypes, ", ")}})
		}
	}
	return nil
}

// Test checks the settings and the key, without changing anything.
func (t *target) Test(ctx context.Context) (contracts.Message, error) {
	if err := t.check(); err != nil {
		return contracts.Message{}, err
	}
	n, err := t.api.count(ctx)
	if err != nil {
		return contracts.Message{}, explain(err, t.endpoint)
	}
	return contracts.Message{Key: i18n.TencentcloudTestOk, Params: map[string]any{"count": n}}, nil
}

// Deploy uploads the version, then has the resources of the certificate it
// follows take it: the last one JEC uploaded, or the first time the one
// set to replace. Certificates JEC uploaded before that are deleted, if it
// is set to; the one being replaced goes the next time, once Tencent Cloud
// is done with it.
func (t *target) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if err := t.check(); err != nil {
		return none, err
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
	id, err := t.api.upload(ctx, alias, string(cryptox.EncodeCertificates(req.Chain...)), string(key))
	if err != nil {
		return none, explain(err, t.endpoint)
	}
	log(contracts.LogLine{Level: contracts.OK, Key: i18n.TencentcloudLogUploaded, Params: map[string]any{"id": id}})

	old := t.replace
	if len(req.Previous) > 0 {
		old = req.Previous[0]
	}
	switch {
	case old == id:
		// This version is there already, as after a deployment tried again.
	case old != "":
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.TencentcloudLogUpdate, Params: map[string]any{"old": old, "id": id, "resources": strings.Join(t.types, ", ")}})
		record, err := t.updated(ctx, old, id)
		if err != nil {
			return none, err
		}
		log(contracts.LogLine{Level: contracts.OK, Key: i18n.TencentcloudLogUpdating, Params: map[string]any{"record": record}})
	default:
		log(contracts.LogLine{Level: contracts.Warn, Key: i18n.TencentcloudLogNoOld, Params: map[string]any{"id": id}})
	}

	var removed []string
	if t.deleteOld {
		for _, ref := range req.Previous {
			if ref == id || ref == old {
				continue
			}
			if err := t.api.remove(ctx, ref); err != nil {
				log(contracts.LogLine{Level: contracts.Warn, Key: i18n.TencentcloudLogDeleteFailed, Params: map[string]any{"id": ref}, Detail: err.Error()})
				continue
			}
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.TencentcloudLogDeleted, Params: map[string]any{"id": ref}})
			removed = append(removed, ref)
		}
	}
	return contracts.DeployResult{Ref: id, Removed: removed}, nil
}

// updated has Tencent Cloud make the task that updates the resources of
// old, asking again while it is being made, and returns its ID.
func (t *target) updated(ctx context.Context, old, id string) (int64, error) {
	for i := 1; ; i++ {
		record, err := t.api.update(ctx, old, id, t.types, t.regions)
		switch {
		case err != nil:
			return 0, explain(err, t.endpoint)
		case record > 0:
			return record, nil
		case i == updateTries:
			return 0, &contracts.Problem{Key: i18n.TencentcloudErrorUpdateSlow}
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(updatePoll):
		}
	}
}

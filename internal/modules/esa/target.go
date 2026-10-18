package esa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

var regions = []providers.Option{
	{Value: regionChina, Label: i18n.EsaRegionChina},
	{Value: regionGlobal, Label: i18n.EsaRegionGlobal},
}

func siteType() providers.Type {
	return providers.Type{
		ID: "esa.site", Kind: providers.Target, Name: i18n.EsaSiteName, Description: i18n.EsaSiteDescription,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "region", Label: i18n.EsaFieldRegion, Kind: providers.Select, Required: true, Default: regionChina, Options: regions},
			{Key: "access_key_id", Label: i18n.EsaFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "access_key_secret", Label: i18n.EsaFieldAccessKeySecret, Help: i18n.EsaFieldAccessKeySecretHelp,
				Kind: providers.Text, Secret: true, Required: true},
			{Key: "site", Label: i18n.EsaFieldSite, Help: i18n.EsaFieldSiteHelp, Kind: providers.Text, Required: true},
			{Key: "delete_old", Label: i18n.EsaFieldDeleteOld, Help: i18n.EsaFieldDeleteOldHelp, Kind: providers.Switch, Default: "true"},
		},
		Credential: credentials.Use(credentials.Aliyun),
	}
}

// target is a site of an ESA account at work.
type target struct {
	api       siteAPI
	site      string
	deleteOld bool
}

var (
	_ contracts.Target = (*target)(nil)
	_ contracts.Tester = (*target)(nil)
)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	api, err := m.newAPI(in)
	if err != nil {
		return nil, err
	}
	return &target{api: api, site: strings.TrimSpace(in.Config["site"]), deleteOld: in.Config["delete_old"] != "false"}, nil
}

// Close has nothing to release.
func (*target) Close() error { return nil }

// Test finds the site and reads its certificates, which takes the
// permissions a deployment needs to begin with.
func (t *target) Test(ctx context.Context) (contracts.Message, error) {
	site, err := t.api.siteID(ctx, t.site)
	if err != nil {
		return contracts.Message{}, t.explain(err)
	}
	held, err := t.api.certificates(ctx, site)
	if err != nil {
		return contracts.Message{}, t.explain(err)
	}
	return contracts.Message{Key: i18n.EsaTestOk, Params: map[string]any{"site": t.site, "count": len(held)}}, nil
}

// Deploy uploads the certificate to the site, unless an earlier attempt
// did, then removes the versions deployed before when set to. A site that
// is full gets room by removing the oldest of those first.
func (t *target) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("esa: no certificate to deploy"))
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.EsaLogSite, Params: map[string]any{"site": t.site}})
	site, err := t.api.siteID(ctx, t.site)
	if err != nil {
		return none, t.explain(err)
	}

	sum := sha256.Sum256(req.Chain[0].Raw)
	fingerprint := hex.EncodeToString(sum[:])
	held, err := t.api.certificates(ctx, site)
	if err != nil {
		return none, t.explain(err)
	}
	var removed []string
	id := ""
	if i := slices.IndexFunc(held, func(c certificate) bool { return c.Fingerprint == fingerprint }); i >= 0 {
		id = held[i].ID
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.EsaLogReuse, Params: map[string]any{"id": id}})
	} else {
		if id, removed, err = t.upload(ctx, site, fingerprint, req, log); err != nil {
			return none, err
		}
	}

	if t.deleteOld {
		for _, ref := range req.Previous {
			if ref == id || slices.Contains(removed, ref) {
				continue
			}
			if !slices.ContainsFunc(held, func(c certificate) bool { return c.ID == ref }) {
				removed = append(removed, ref) // gone already
				continue
			}
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.EsaLogDelete, Params: map[string]any{"id": ref}})
			if err := t.api.remove(ctx, site, ref); err != nil {
				// The new certificate is in place; the next deployment tries again.
				log(contracts.LogLine{Level: contracts.Warn, Key: i18n.EsaLogDeleteFailed, Params: map[string]any{"id": ref}, Detail: err.Error()})
				continue
			}
			removed = append(removed, ref)
		}
	}
	return contracts.DeployResult{Ref: id, Removed: removed}, nil
}

// upload adds the certificate to the site. When the site is full and old
// versions may go, the oldest goes first, once.
func (t *target) upload(ctx context.Context, site int64, fingerprint string, req contracts.DeployRequest,
	log func(contracts.LogLine),
) (string, []string, error) {
	keyPEM, err := cryptox.EncodeKeyTraditional(req.Key)
	if err != nil {
		return "", nil, retry.Permanent(err)
	}
	key := string(keyPEM)
	chain := string(cryptox.EncodeCertificates(req.Chain...))
	name := "jec-" + strconv.FormatInt(req.CertificateID, 10) + "-" + fingerprint[:12]
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.EsaLogUpload, Params: map[string]any{"name": name}})
	id, err := t.api.upload(ctx, site, name, chain, key)
	var removed []string
	if err != nil && isQuota(err) && t.deleteOld && len(req.Previous) > 0 {
		oldest := req.Previous[len(req.Previous)-1]
		log(contracts.LogLine{Level: contracts.Warn, Key: i18n.EsaLogMakeRoom, Params: map[string]any{"id": oldest}})
		if rerr := t.api.remove(ctx, site, oldest); rerr != nil {
			return "", nil, t.explain(rerr)
		}
		removed = append(removed, oldest)
		id, err = t.api.upload(ctx, site, name, chain, key)
	}
	if err != nil {
		return "", removed, t.explain(err)
	}
	log(contracts.LogLine{Level: contracts.OK, Key: i18n.EsaLogUploaded, Params: map[string]any{"id": id}})
	return id, removed, nil
}

// explain turns an error of ESA's API into a Problem the UI can show,
// marked for retrying or not.
func (t *target) explain(err error) error {
	switch {
	case errors.Is(err, errSiteNotFound):
		return retry.Permanent(&contracts.Problem{Key: i18n.EsaErrorSite, Params: map[string]any{"site": t.site}, Err: err})
	case isQuota(err):
		// The site holds all the custom certificates its plan allows.
		return retry.Permanent(&contracts.Problem{Key: i18n.EsaErrorQuota, Params: map[string]any{"site": t.site}, Err: err})
	}
	return apiProblem(err)
}

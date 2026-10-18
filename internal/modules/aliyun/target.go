package aliyun

import (
	"context"
	"errors"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

func (svc service) providerType() providers.Type {
	desc := i18n.AliyunCdnDescription
	if svc.id == dcdn.id {
		desc = i18n.AliyunDcdnDescription
	}
	return providers.Type{
		ID: "aliyun." + svc.id, Kind: providers.Target, Name: svc.name, Description: desc,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.AliyunFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "access_key_secret", Label: i18n.AliyunFieldAccessKeySecret, Help: svc.secretHelp(), Kind: providers.Text, Secret: true, Required: true},
			{Key: "domains", Label: i18n.AliyunFieldDomains, Help: i18n.AliyunFieldDomainsHelp, Kind: providers.Text},
		},
	}
}

// secretHelp names the permissions a RAM user needs.
func (svc service) secretHelp() i18n.Key {
	if svc.id == dcdn.id {
		return i18n.AliyunFieldDcdnSecretHelp
	}
	return i18n.AliyunFieldCdnSecretHelp
}

// target is a CDN or DCDN of an account.
type target struct {
	svc     service
	api     cdnAPI
	domains []string
}

var (
	_ contracts.Target = (*target)(nil)
	_ contracts.Tester = (*target)(nil)
)

func (m *Module) factory(svc service) providers.Factory {
	return func(_ context.Context, in providers.Instance) (any, error) {
		api, err := m.open(svc, strings.TrimSpace(in.Value("access_key_id")), strings.TrimSpace(in.Value("access_key_secret")))
		if err != nil {
			return nil, retry.Permanent(err)
		}
		return &target{svc: svc, api: api, domains: split(in.Config["domains"])}, nil
	}
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
func (*target) Close() error { return nil }

// Test makes sure the account has the accelerated domains, without
// changing them.
func (t *target) Test(ctx context.Context) (contracts.Message, error) {
	if len(t.domains) == 0 {
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorNoDomains})
	}
	for _, d := range t.domains {
		if err := t.api.check(ctx, d); err != nil {
			return contracts.Message{}, explain(err, t.svc, d)
		}
	}
	return contracts.Message{Key: i18n.AliyunTestOk, Params: map[string]any{"domains": strings.Join(t.domains, ", ")}}, nil
}

// Deploy sets the certificate on each accelerated domain: those set, or
// else the certificate's own, but for wildcards. Alibaba Cloud keeps the
// certificate in its Certificate Management Service too; there is nothing
// for JEC to remove afterwards.
func (t *target) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("aliyun: no certificate to deploy"))
	}
	domains := t.domains
	if len(domains) == 0 {
		for _, d := range req.Domains {
			if !strings.HasPrefix(d, "*.") {
				domains = append(domains, d)
			}
		}
	}
	if len(domains) == 0 {
		return none, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorNoDomains})
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	chain := string(cryptox.EncodeCertificates(req.Chain...))
	for _, d := range domains {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogSet, Params: map[string]any{"domain": d}})
		if err := t.api.setCertificate(ctx, d, chain, string(key)); err != nil {
			return none, explain(err, t.svc, d)
		}
	}
	return none, nil
}

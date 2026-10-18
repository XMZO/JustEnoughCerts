package lego

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnsrecord"
)

// catalogJSON describes lego's DNS providers; tools/legocatalog writes it
// from the lego module that go.mod requires.
//
//go:embed catalog.json
var catalogJSON []byte

type catalogKey struct {
	Key    string `json:"key"`
	Doc    string `json:"doc"`
	Secret bool   `json:"secret"`
}

type catalogProvider struct {
	Code        string       `json:"code"`
	Name        string       `json:"name"`
	URL         string       `json:"url"`
	Description string       `json:"description"`
	Credentials []catalogKey `json:"credentials"`
	Additional  []catalogKey `json:"additional"`
}

type catalog struct {
	Lego      string            `json:"lego"`
	Providers []catalogProvider `json:"providers"`
}

func loadCatalog() (*catalog, error) {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		return nil, fmt.Errorf("lego catalog: %w", err)
	}
	return &c, nil
}

// Groups for the UI (PLAN.md 5.3.2): providers common in China, popular
// ones elsewhere, and those people run themselves.
var (
	china = []string{
		"alidns", "aliesa", "tencentcloud", "edgeone", "huaweicloud", "baiducloud", "jdcloud",
		"volcengine", "westcn", "xinnet", "dnsla", "rainyun", "ucloud", "todaynic",
	}
	popular = []string{
		"cloudflare", "route53", "gcloud", "azuredns", "digitalocean", "linode", "vultr", "hetzner",
		"ovh", "gandiv5", "godaddy", "namecheap", "spaceship", "porkbun", "namedotcom", "namesilo",
		"dynadot", "desec", "duckdns", "netlify", "vercel", "bunny", "ns1",
	}
	selfHosted = []string{"pdns", "dnsupdate", "acmedns", "httpreq"}
)

// keys returns the configuration keys of p, credentials first.
func (p catalogProvider) keys() []catalogKey {
	return append(slices.Clone(p.Credentials), p.Additional...)
}

// providerType describes p for the registry. lego's descriptions are its
// own English text, which the UI shows as it is. Its additional keys, such
// as timeouts and TTLs, are advanced settings.
func providerType(p catalogProvider) providers.Type {
	t := providers.Type{
		ID: "lego." + p.Code, Kind: providers.Solver,
		Brand: p.Name, Doc: p.Description, URL: p.URL,
		Fields: []providers.Field{},
		// JEC adds records of any type through some providers itself.
		Records: slices.Contains(dnsrecord.Codes, p.Code),
	}
	switch {
	case slices.Contains(china, p.Code):
		t.Tags = []string{"china"}
	case slices.Contains(popular, p.Code):
		t.Tags = []string{"popular"}
	case slices.Contains(selfHosted, p.Code):
		t.Tags = []string{"self_hosted"}
	}
	for i, k := range p.keys() {
		t.Fields = append(t.Fields, providers.Field{
			Key: k.Key, Doc: k.Doc, Kind: providers.Text, Secret: k.Secret, Advanced: i >= len(p.Credentials),
		})
	}
	t.Credential = credentialOf(p.Code)
	return t
}

// credentialOf is how lego's provider code takes the keys of a cloud that
// JEC deploys to too, so that one credential serves both: the environment
// variables the kind's fields are; nil for the other providers.
func credentialOf(code string) *providers.CredentialUse {
	switch code {
	case "alidns":
		return credentials.Use(credentials.Aliyun, "ALICLOUD_ACCESS_KEY", "ALICLOUD_SECRET_KEY")
	case "aliesa":
		return credentials.Use(credentials.Aliyun, "ALIESA_ACCESS_KEY", "ALIESA_SECRET_KEY")
	case "tencentcloud":
		return credentials.Use(credentials.TencentCloud, "TENCENTCLOUD_SECRET_ID", "TENCENTCLOUD_SECRET_KEY")
	case "edgeone":
		return credentials.Use(credentials.TencentCloud, "EDGEONE_SECRET_ID", "EDGEONE_SECRET_KEY")
	case "cloudflare":
		return credentials.Use(credentials.Cloudflare, "CF_DNS_API_TOKEN")
	case "route53":
		return credentials.Use(credentials.AWS, "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY")
	case "huaweicloud":
		return credentials.Use(credentials.HuaweiCloud, "HUAWEICLOUD_ACCESS_KEY_ID", "HUAWEICLOUD_SECRET_ACCESS_KEY")
	case "volcengine":
		return credentials.Use(credentials.Volcengine, "VOLC_ACCESSKEY", "VOLC_SECRETKEY")
	case "baiducloud":
		return credentials.Use(credentials.BaiduCloud, "BAIDUCLOUD_ACCESS_KEY_ID", "BAIDUCLOUD_SECRET_ACCESS_KEY")
	}
	return nil
}

// factory makes the Solver of an instance of p: a child process whose
// environment holds the instance's settings. For a provider JEC adds
// records of any type through itself, it is a contracts.RecordSolver too.
func factory(p catalogProvider, log *slog.Logger) providers.Factory {
	keys := p.keys()
	own := slices.Contains(dnsrecord.Codes, p.Code)
	return func(_ context.Context, in providers.Instance) (any, error) {
		var env []string
		settings := map[string]string{}
		for _, k := range keys {
			if v := in.Value(k.Key); v != "" {
				env = append(env, k.Key+"="+v)
				settings[k.Key] = v
			}
		}
		s := newSolver(p.Code, env, log.With("provider", p.Code, "instance", in.ID))
		if !own {
			return s, nil
		}
		return &records{solver: s, settings: settings}, nil
	}
}

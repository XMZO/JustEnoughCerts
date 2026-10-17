package lego

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
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
	return t
}

// factory makes the Solver of an instance of p: a child process whose
// environment holds the instance's settings.
func factory(p catalogProvider, log *slog.Logger) providers.Factory {
	keys := p.keys()
	return func(_ context.Context, in providers.Instance) (any, error) {
		var env []string
		for _, k := range keys {
			if v := in.Value(k.Key); v != "" {
				env = append(env, k.Key+"="+v)
			}
		}
		return newSolver(p.Code, env, log.With("provider", p.Code, "instance", in.ID)), nil
	}
}

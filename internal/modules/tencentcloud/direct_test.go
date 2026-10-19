package tencentcloud

import (
	"slices"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

func openDirect(t *testing.T, api *fakeAPI, resource string, config map[string]string) (*direct, error) {
	t.Helper()
	m := &Module{open: func(string, string, string) (sslAPI, error) { return api, nil }}
	p, err := m.directFactory(resource)(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"secret_id": "AKID-id", "secret_key": "s3cret"}})
	if err != nil {
		return nil, err
	}
	return p.(*direct), nil
}

func TestDirect(t *testing.T) {
	api := &fakeAPI{certs: map[string]string{"cert-older": "OLDER", "cert-before": "BEFORE"}}

	// CDN: the version goes to the domains; of those JEC uploaded before,
	// all go but the one the domains had, which goes the next time.
	cdn, err := openDirect(t, api, "cdn", map[string]string{"hosts": "example.com, WWW.example.com", "delete_old": "true"})
	if err != nil {
		t.Fatal(err)
	}
	req := request(t)
	req.Previous = []string{"cert-before", "cert-older"}
	res, err := cdn.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(api.deployed, []string{"cdn:example.com,www.example.com>" + res.Ref}) || !slices.Equal(res.Removed, []string{"cert-older"}) {
		t.Errorf("deployed %v, removed %v", api.deployed, res.Removed)
	}

	// EdgeOne: the hosts of the zone take it.
	teo, err := openDirect(t, api, "teo", map[string]string{"zone": "zone-2o1abcd", "hosts": "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	res, err = teo.Deploy(t.Context(), request(t))
	if err != nil || !slices.Equal(api.bound, []string{"zone-2o1abcd:example.com>" + res.Ref}) {
		t.Errorf("bound %v: %v", api.bound, err)
	}

	// Settings that cannot work are refused at once.
	for _, c := range []struct {
		resource string
		config   map[string]string
	}{
		{"cdn", map[string]string{}},
		{"teo", map[string]string{"zone": "example.com", "hosts": "example.com"}},
	} {
		if _, err := openDirect(t, api, c.resource, c.config); err == nil {
			t.Errorf("%s %v: opened", c.resource, c.config)
		}
	}
	var _ contracts.Target = cdn
}

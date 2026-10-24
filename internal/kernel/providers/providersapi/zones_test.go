package providersapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

// fakeZones holds example.com. The zone of saas.example.com is
// cloudflare.com, where its _acme-challenge points, as a provider finds
// by following the CNAME.
type fakeZones struct{}

func (fakeZones) ZoneOf(_ context.Context, name string) (string, bool, error) {
	switch name {
	case "saas.example.com", "*.saas.example.com":
		return "cloudflare.com", false, nil
	case "other.org":
		return "other.org", false, nil
	}
	return "example.com", true, nil
}

func (fakeZones) Close() error { return nil }

// TestZonesDelegated: a zone not held, and not the domain's own, comes
// with where _acme-challenge points, the likely cause; the others without
// a look-up.
func TestZonesDelegated(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := providers.New(st.DB(), box.Scope("kernel"), func(string) bool { return true })
	typ := providers.Type{ID: "fake.zones", Kind: providers.Solver, Brand: "Zones", Zones: true}
	if err := reg.Scope("fake").Add(typ, func(context.Context, providers.Instance) (any, error) { return fakeZones{}, nil }); err != nil {
		t.Fatal(err)
	}
	reg.Seal()
	_, a := humatest.New(t)
	Register(a, reg)
	saved := delegated
	t.Cleanup(func() { delegated = saved })
	var asked []string
	delegated = func(_ context.Context, domain string) string {
		asked = append(asked, domain)
		return "saas.example.com.0123abcd.dcv.cloudflare.com"
	}
	id, err := reg.Create(t.Context(), "fake.zones", "zones", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp := a.Get("/providers/" + strconv.FormatInt(id, 10) + "/zones?domain=www.example.com&domain=*.saas.example.com&domain=other.org")
	var out struct{ Domains []ZoneBody }
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &out) != nil || len(out.Domains) != 3 {
		t.Fatalf("zones = %d %s", resp.Code, resp.Body)
	}
	vias := []string{out.Domains[0].Via, out.Domains[1].Via, out.Domains[2].Via}
	if !slices.Equal(vias, []string{"", "saas.example.com.0123abcd.dcv.cloudflare.com", ""}) {
		t.Errorf("vias %q", vias)
	}
	if !slices.Equal(asked, []string{"*.saas.example.com"}) {
		t.Errorf("looked up %v", asked)
	}
}

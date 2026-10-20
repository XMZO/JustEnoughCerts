package watch

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

var start = time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)

type fixture struct {
	t   *testing.T
	m   *Module
	api humatest.TestAPI
	ca  *cryptoxtest.CA
	now time.Time
	// served is what each address serves; an address missing does not answer.
	served map[string][]*x509.Certificate
	asked  []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	m := New()
	if _, err := st.Migrate(t.Context(), "watch", m.Migrations()); err != nil {
		t.Fatal(err)
	}
	bus := events.New(st.DB(), slog.New(slog.DiscardHandler), func(string) bool { return true })
	bus.Seal()
	f := &fixture{t: t, m: m, ca: cryptoxtest.NewCA(t), now: start, served: map[string][]*x509.Certificate{}}
	m.db, m.events = st.DB(), bus.Scope("watch")
	m.now = func() time.Time { return f.now }
	m.roots = x509.NewCertPool()
	m.roots.AddCert(f.ca.Root)
	m.fetch = func(_ context.Context, host, address string) ([]*x509.Certificate, error) {
		f.asked = append(f.asked, host+" "+address)
		if certs, ok := f.served[address]; ok {
			return certs, nil
		}
		return nil, errors.New("connection refused")
	}
	_, f.api = humatest.New(t)
	registerAPI(f.api, m)
	return f
}

// serve has address serve a certificate for names that ends in days.
func (f *fixture) serve(address string, days int, names ...string) {
	key := cryptoxtest.NewKey(f.t)
	leaf := f.ca.Leaf(f.t, key, f.now.Add(-time.Hour), f.now.Add(time.Duration(days)*24*time.Hour), names...)
	f.served[address] = []*x509.Certificate{leaf, f.ca.Intermediate}
}

func (f *fixture) site(id int64) SiteBody {
	f.t.Helper()
	out, err := f.m.one(f.t.Context(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return out.Body
}

func (f *fixture) told() []contracts.SiteEvent {
	f.t.Helper()
	rows, err := f.m.db.QueryContext(f.t.Context(), "SELECT data FROM kernel_events WHERE type = ? ORDER BY id", contracts.SiteExpiring)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []contracts.SiteEvent
	for rows.Next() {
		var data string
		var e contracts.SiteEvent
		if err := rows.Scan(&data); err != nil || json.Unmarshal([]byte(data), &e) != nil {
			f.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestParseHost(t *testing.T) {
	for in, want := range map[string]string{
		"example.com":                    "example.com:443",
		"  Example.COM.  ":               "example.com:443",
		"https://shop.example.com/a?b=c": "shop.example.com:443",
		"https://shop.example.com:8443/": "shop.example.com:8443",
		"example.com:8443":               "example.com:8443",
		"\u4f8b\u5b50.example":           "xn--fsqu00a.example:443",
		"192.0.2.1":                      "192.0.2.1:443",
		"[2001:db8::1]:9443":             "2001:db8::1:9443",
		"example.com/path":               "example.com:443",
		"localhost":                      "localhost:443",
	} {
		host, port, ok := parseHost(in)
		if !ok || host+":"+strconv.Itoa(port) != want {
			t.Errorf("parseHost(%q) = %q %d %v, want %s", in, host, port, ok, want)
		}
	}
	for _, bad := range []string{"", "nodot", "example.com:0", "example.com:70000", "https://", "exa mple.com"} {
		if _, _, ok := parseHost(bad); ok {
			t.Errorf("parseHost(%q) took it", bad)
		}
	}
}

func TestSites(t *testing.T) {
	f := newFixture(t)
	f.serve("example.com:443", 90, "example.com", "www.example.com")

	// Added, it is checked at once.
	resp := f.api.Post("/sites", map[string]any{"host": "https://example.com/", "name": "Shop"})
	var s SiteBody
	if resp.Code != http.StatusCreated || json.Unmarshal(resp.Body.Bytes(), &s) != nil {
		t.Fatalf("add = %d %s", resp.Code, resp.Body)
	}
	if s.State != stateOK || s.Host != "example.com" || s.Port != 443 || s.Name != "Shop" || s.NotAfter == nil ||
		!slices.Equal(s.Names, []string{"example.com", "www.example.com"}) || s.Issuer == "" || !s.CheckAt.Equal(start.Add(every)) {
		t.Errorf("added %+v", s)
	}
	if resp := f.api.Post("/sites", map[string]any{"host": "example.com:443"}); resp.Code != http.StatusConflict {
		t.Errorf("added twice: %d", resp.Code)
	}
	if resp := f.api.Post("/sites", map[string]any{"host": "not a host"}); resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("a bad host: %d", resp.Code)
	}

	// Its certificate comes close to its end: each level is told once.
	f.now = start.Add(70 * 24 * time.Hour)
	for range 2 {
		if err := f.m.check(t.Context(), s.ID); err != nil {
			t.Fatal(err)
		}
	}
	f.now = start.Add(85 * 24 * time.Hour)
	if err := f.m.check(t.Context(), s.ID); err != nil {
		t.Fatal(err)
	}
	told := f.told()
	if len(told) != 2 || told[0].Expiry != contracts.ExpiryWindow || told[1].Expiry != contracts.ExpiryUrgent ||
		told[0].Host != "example.com" || told[0].Name != "Shop" || f.site(s.ID).State != stateExpiring {
		t.Errorf("told %+v, state %s", told, f.site(s.ID).State)
	}
	// A new certificate is told of anew.
	f.serve("example.com:443", 10, "example.com")
	if err := f.m.check(t.Context(), s.ID); err != nil || len(f.told()) != 3 {
		t.Errorf("a new certificate: %v, told %d", err, len(f.told()))
	}

	// One that does not answer keeps what it served, and is tried again soon.
	delete(f.served, "example.com:443")
	if err := f.m.check(t.Context(), s.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.site(s.ID); got.State != stateUnreachable || got.Error == "" || got.NotAfter == nil || !got.CheckAt.Equal(f.now.Add(retry)) {
		t.Errorf("unreachable %+v", got)
	}

	// One for other names, on its own port, is not valid.
	f.serve("other.example.com:8443", 90, "elsewhere.example.com")
	resp = f.api.Post("/sites", map[string]any{"host": "other.example.com:8443"})
	if resp.Code != http.StatusCreated || json.Unmarshal(resp.Body.Bytes(), &s) != nil || s.State != stateInvalid || s.Error == "" {
		t.Errorf("invalid = %d %+v", resp.Code, s)
	}

	// Due ones are checked by the job.
	f.asked = nil
	f.now = f.now.Add(every)
	if err := f.m.checkDue(t.Context()); err != nil || len(f.asked) != 2 {
		t.Errorf("due: %v, asked %v", err, f.asked)
	}
	if resp := f.api.Delete("/sites/" + strconv.FormatInt(s.ID, 10)); resp.Code != http.StatusNoContent {
		t.Errorf("delete = %d", resp.Code)
	}
	var list listOutput
	if err := json.Unmarshal(f.api.Get("/sites").Body.Bytes(), &list.Body); err != nil || len(list.Body.Sites) != 1 {
		t.Errorf("list %+v: %v", list.Body, err)
	}
}

func TestSiteWithoutIntermediates(t *testing.T) {
	f := newFixture(t)
	f.serve("example.com:443", 90, "example.com")
	f.served["example.com:443"] = f.served["example.com:443"][:1] // the leaf alone
	fetched := false
	f.m.issuers = func(_ context.Context, b *cryptox.Bundle) error {
		if !fetched {
			return errors.New("the CA's address did not answer")
		}
		b.Chain = append(b.Chain, f.ca.Intermediate)
		return nil
	}
	resp := f.api.Post("/sites", map[string]any{"host": "example.com"})
	var s SiteBody
	if resp.Code != http.StatusCreated || json.Unmarshal(resp.Body.Bytes(), &s) != nil {
		t.Fatalf("add = %d %s", resp.Code, resp.Body)
	}
	if s.State != stateInvalid || s.Incomplete {
		t.Fatalf("not completed: %+v", s)
	}
	// Completed as the leaf names its issuer: trusted, but told.
	fetched = true
	if resp := f.api.Post("/sites/" + strconv.FormatInt(s.ID, 10) + "/check"); resp.Code != http.StatusOK {
		t.Fatalf("check = %d %s", resp.Code, resp.Body)
	}
	if s := f.site(s.ID); s.State != stateOK || !s.Incomplete || s.Error != "" {
		t.Fatalf("completed: %+v", s)
	}
}

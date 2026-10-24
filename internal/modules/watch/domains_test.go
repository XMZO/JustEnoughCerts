package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

func TestRegistered(t *testing.T) {
	for in, want := range map[string]string{
		"shop.com":               "shop.com",
		"www.Shop.COM.":          "shop.com",
		"*.cart.shop.com":        "shop.com",
		"a.b.shop.co.uk":         "shop.co.uk",
		"www.example.com":        "",
		"example.org":            "",
		"user.github.io":         "github.io",
		"x.y.blogspot.co.uk":     "blogspot.co.uk",
		"xn--fsqu00a.xn--fiqs8s": "xn--fsqu00a.xn--fiqs8s",
		"com":                    "",
		"co.uk":                  "",
		"nas.lan":                "",
		"localhost":              "",
		"192.0.2.1":              "",
		"2001:db8::1":            "",
		"":                       "",
	} {
		if got := registered(in); got != want {
			t.Errorf("registered(%q) = %q, want %q", in, got, want)
		}
	}
}

// registry is a fake of IANA's bootstrap file and of the RDAP server of
// com and uk.
type registry struct {
	mu         sync.Mutex
	ends       map[string]time.Time // a domain missing is not registered
	registrar  string
	bootstraps int
	asked      []string
}

func (r *registry) set(domain string, end time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends[domain] = end
}

func (r *registry) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if req.URL.Path == "/dns.json" {
			r.bootstraps++
			_, _ = fmt.Fprintf(w, `{"version":"1.0","services":[[["com","uk"],["http://%s/rdap"]],[["net"],[]]]}`, req.Host)
			return
		}
		name, ok := strings.CutPrefix(req.URL.Path, "/rdap/domain/")
		if !ok {
			http.NotFound(w, req)
			return
		}
		r.asked = append(r.asked, name)
		end, ok := r.ends[name]
		if !ok {
			http.NotFound(w, req)
			return
		}
		events := `[{"eventAction":"registration","eventDate":"2020-01-01T00:00:00Z"}`
		if !end.IsZero() {
			events += `,{"eventAction":"expiration","eventDate":"` + end.Format(time.RFC3339) + `"}`
		}
		_, _ = fmt.Fprintf(w, `{"objectClassName":"domain","ldhName":%q,"events":%s],"entities":[
			{"objectClassName":"entity","roles":["registrant"],"vcardArray":["vcard",[["fn",{},"text","Someone"]]]},
			{"objectClassName":"entity","roles":["registrar"],"vcardArray":["vcard",[["version",{},"text","4.0"],["fn",{},"text",%q]]]}]}`,
			name, events, r.registrar)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// certificate is what the fake API tells of a certificate.
type certificate struct {
	ID      int64    `json:"id"`
	Domains []string `json:"domains"`
}

func newDomainFixture(t *testing.T) (*fixture, *registry, *[]certificate, *bool) {
	f := newFixture(t)
	reg := &registry{ends: map[string]time.Time{}, registrar: "Example Registrar, Inc."}
	srv := reg.serve(t)
	f.m.rdap.client, f.m.rdap.bootstrap = srv.Client(), srv.URL+"/dns.json"
	f.m.rdap.whois = fakeWhois
	certs := &[]certificate{}
	off := new(bool)
	f.m.call = func(_ context.Context, via, method, path string, _, out any) error {
		if name, ok := strings.CutPrefix(path, "/whoice/domains/"); ok && via == "watch" && method == http.MethodGet {
			body, ok := f.more[name]
			if !ok {
				return &module.CallError{Status: http.StatusNotFound}
			}
			data, _ := json.Marshal(body)
			return json.Unmarshal(data, out)
		}
		if via != "watch" || method != http.MethodGet || path != "/certs/certificates" {
			t.Errorf("asked %s %s as %s", method, path, via)
		}
		if *off {
			return &module.CallError{Status: http.StatusNotFound}
		}
		data, _ := json.Marshal(map[string]any{"certificates": *certs})
		return json.Unmarshal(data, out)
	}
	return f, reg, certs, off
}

func (f *fixture) domain(name string) DomainBody {
	f.t.Helper()
	out, err := f.m.oneDomain(f.t.Context(), name)
	if err != nil {
		f.t.Fatalf("domain %s: %v", name, err)
	}
	return out.Body
}

func (f *fixture) domainNames() []string {
	f.t.Helper()
	list, err := f.m.domains(f.t.Context(), "")
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, d := range list {
		out = append(out, d.Name)
	}
	return out
}

func (f *fixture) domainsTold() []contracts.DomainEvent {
	f.t.Helper()
	rows, err := f.m.db.QueryContext(f.t.Context(), "SELECT data FROM kernel_events WHERE type = ? ORDER BY id", contracts.DomainExpiring)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []contracts.DomainEvent
	for rows.Next() {
		var data string
		var e contracts.DomainEvent
		if err := rows.Scan(&data); err != nil || json.Unmarshal([]byte(data), &e) != nil {
			f.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (f *fixture) runDomains() {
	f.t.Helper()
	if err := f.m.domainsDue(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
}

func TestDomains(t *testing.T) {
	f, reg, certs, off := newDomainFixture(t)
	day := 24 * time.Hour
	reg.set("shop.com", start.Add(20*day))
	reg.set("shop.co.uk", time.Time{}) // registered, no end told
	*certs = []certificate{
		{ID: 1, Domains: []string{"shop.com", "www.shop.com", "*.shop.shop.com"}},
		{ID: 2, Domains: []string{"api.shop.co.uk"}},
		{ID: 3, Domains: []string{"nas.lan", "192.0.2.1"}},
	}
	f.serve("blog.other.com:443", 90, "blog.other.com")
	if resp := f.api.Post("/sites", map[string]any{"host": "blog.other.com"}); resp.Code != http.StatusCreated {
		t.Fatalf("add site = %d %s", resp.Code, resp.Body)
	}

	f.runDomains()
	if got := f.domainNames(); !slices.Equal(got, []string{"other.com", "shop.co.uk", "shop.com"}) {
		t.Fatalf("domains = %v", got)
	}
	d := f.domain("shop.com")
	if d.State != stateExpiring || d.ExpiresAt == nil || !d.ExpiresAt.Equal(start.Add(20*day)) || d.Registrar != "Example Registrar, Inc." ||
		!slices.Equal(d.Certificates, []int64{1}) || len(d.Sites) != 0 || d.Manual || d.CheckedAt == nil {
		t.Errorf("shop.com = %+v", d)
	}
	if d := f.domain("shop.co.uk"); d.State != stateUnknown || d.ExpiresAt != nil || !strings.Contains(d.Error, "expiration") || d.Cause != causeNoEnd ||
		!d.CheckAt.Equal(start.Add(domainUnknown)) {
		t.Errorf("shop.co.uk = %+v", d)
	}
	if d := f.domain("other.com"); d.State != stateFailed || !strings.Contains(d.Error, "404") || d.Cause != "" || len(d.Sites) != 1 ||
		!d.CheckAt.Equal(start.Add(domainRetry)) {
		t.Errorf("other.com = %+v", d)
	}
	told := f.domainsTold()
	if len(told) != 1 || told[0].Domain != "shop.com" || told[0].Expiry != contracts.ExpiryWindow ||
		!slices.Equal(told[0].Certificates, []int64{1}) || told[0].Registrar != "Example Registrar, Inc." {
		t.Fatalf("told %+v", told)
	}

	// Nothing is due a minute on.
	reg.asked = nil
	f.now = start.Add(time.Minute)
	f.runDomains()
	if len(reg.asked) != 0 {
		t.Errorf("asked again: %v", reg.asked)
	}

	// Two weeks on, a week is left: told again, once.
	f.now = start.Add(14 * day)
	f.runDomains()
	f.now = start.Add(15 * day)
	f.runDomains()
	if told := f.domainsTold(); len(told) != 2 || told[1].Expiry != contracts.ExpiryUrgent {
		t.Fatalf("told %+v", told)
	}
	// Renewed for a year: ok, and a new end is told anew.
	reg.set("shop.com", start.Add(385*day))
	f.now = start.Add(16 * day)
	f.runDomains()
	if d := f.domain("shop.com"); d.State != stateOK || !d.CheckAt.Equal(f.now.Add(domainEvery)) {
		t.Errorf("renewed = %+v", d)
	}
	f.now = start.Add(370 * day)
	f.runDomains()
	if told := f.domainsTold(); len(told) != 3 || told[2].Expiry != contracts.ExpiryWindow {
		t.Fatalf("told %+v", told)
	}
	if reg.bootstraps < 2 || reg.bootstraps > 6 {
		t.Errorf("the bootstrap file was fetched %d times", reg.bootstraps)
	}

	// While the certificates cannot be listed, what was known stays.
	*off = true
	*certs = nil
	f.runDomains()
	if got := f.domainNames(); !slices.Equal(got, []string{"other.com", "shop.co.uk", "shop.com"}) {
		t.Fatalf("domains while off = %v", got)
	}
	// Then certificate 2 is gone, and with it its domain.
	*off = false
	*certs = []certificate{{ID: 1, Domains: []string{"www.shop.com"}}}
	f.runDomains()
	if got := f.domainNames(); !slices.Equal(got, []string{"other.com", "shop.com"}) {
		t.Fatalf("domains = %v", got)
	}
}

func TestDomainsByHand(t *testing.T) {
	f, reg, certs, _ := newDomainFixture(t)
	reg.set("manual.com", start.Add(200*24*time.Hour))
	reg.set("shop.com", start.Add(300*24*time.Hour))
	*certs = []certificate{{ID: 1, Domains: []string{"shop.com"}}}

	resp := f.api.Post("/domains", map[string]any{"name": "https://www.Manual.com/x"})
	var d DomainBody
	if resp.Code != http.StatusCreated || json.Unmarshal(resp.Body.Bytes(), &d) != nil {
		t.Fatalf("add = %d %s", resp.Code, resp.Body)
	}
	if d.Name != "manual.com" || !d.Manual || d.State != stateOK || d.ExpiresAt == nil {
		t.Errorf("added %+v", d)
	}
	if resp := f.api.Post("/domains", map[string]any{"name": "manual.com"}); resp.Code != http.StatusConflict {
		t.Errorf("add again = %d %s", resp.Code, resp.Body)
	}
	for _, bad := range []string{"192.0.2.1", "nas.lan", "co.uk"} {
		if resp := f.api.Post("/domains", map[string]any{"name": bad}); resp.Code != http.StatusUnprocessableEntity {
			t.Errorf("add %s = %d %s", bad, resp.Code, resp.Body)
		}
	}
	// One a certificate is under is marked; unmarked, it stays.
	f.runDomains()
	if resp := f.api.Post("/domains", map[string]any{"name": "shop.com"}); resp.Code != http.StatusCreated {
		t.Fatalf("add shop.com = %d %s", resp.Code, resp.Body)
	}
	if !f.domain("shop.com").Manual {
		t.Error("shop.com is not marked")
	}
	if resp := f.api.Delete("/domains/shop.com"); resp.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", resp.Code, resp.Body)
	}
	if d := f.domain("shop.com"); d.Manual || !slices.Equal(d.Certificates, []int64{1}) {
		t.Errorf("shop.com = %+v", d)
	}
	if resp := f.api.Delete("/domains/shop.com"); resp.Code != http.StatusNotFound {
		t.Errorf("delete again = %d", resp.Code)
	}
	// The sync keeps one added by hand; deleted, it goes.
	f.runDomains()
	if got := f.domainNames(); !slices.Equal(got, []string{"manual.com", "shop.com"}) {
		t.Fatalf("domains = %v", got)
	}
	if resp := f.api.Delete("/domains/manual.com"); resp.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", resp.Code, resp.Body)
	}
	if got := f.domainNames(); !slices.Equal(got, []string{"shop.com"}) {
		t.Fatalf("domains = %v", got)
	}

	// Looked up now on request.
	reg.set("shop.com", start.Add(10*24*time.Hour))
	resp = f.api.Post("/domains/shop.com/check")
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &d) != nil || d.State != stateExpiring {
		t.Fatalf("check = %d %s", resp.Code, resp.Body)
	}
	if resp := f.api.Post("/domains/nothing.com/check"); resp.Code != http.StatusNotFound {
		t.Errorf("check unknown = %d", resp.Code)
	}
	var list domainsOutput
	resp = f.api.Get("/domains")
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &list.Body) != nil || len(list.Body.Domains) != 1 {
		t.Fatalf("list = %d %s", resp.Code, resp.Body)
	}
}

// fakeWhois answers as IANA's and two registries' WHOIS servers do: .cn
// tells an end, .de none, and IANA names no server for .net.
func fakeWhois(_ context.Context, server, query string) (string, error) {
	switch server + " " + query {
	case "whois.iana.org cn":
		return "domain:       CN\nwhois:        whois.cnnic.cn\n", nil
	case "whois.iana.org de":
		return "whois:        whois.denic.de\nstatus:       ACTIVE\n", nil
	case "whois.cnnic.cn shop.cn":
		return "Domain Name: shop.cn\nSponsoring Registrar: Example Registrar\nExpiration Time: 2029-03-17 12:48:36\n", nil
	case "whois.denic.de shop.de":
		return "Domain: shop.de\nStatus: connect\n", nil
	}
	return "", nil
}

func TestNoRDAP(t *testing.T) {
	f, _, _, _ := newDomainFixture(t)
	reg, err := f.m.rdap.lookup(t.Context(), "shop.cn")
	if err != nil || !reg.expires.Equal(time.Date(2029, 3, 17, 12, 48, 36, 0, time.UTC)) || reg.registrar != "Example Registrar" {
		t.Errorf("shop.cn = %+v %v", reg, err)
	}
	if _, err := f.m.rdap.lookup(t.Context(), "shop.de"); !errors.Is(err, errNoEnd) {
		t.Errorf("shop.de = %v", err)
	}
	if _, err := f.m.rdap.lookup(t.Context(), "shop.net"); !errors.Is(err, errNoRDAP) {
		t.Errorf("shop.net = %v", err)
	}
}

// TestLookupMore: where the lookup here tells no end, module whoice is
// asked: its end is taken, and its reason for none; while it is off or
// fails, the lookup's own error stays.
func TestLookupMore(t *testing.T) {
	f, _, _, _ := newDomainFixture(t)
	end := time.Date(2028, 5, 1, 0, 0, 0, 0, time.UTC)
	f.more = map[string]map[string]any{
		"shop.de":  {"name": "shop.de", "status": "registered", "expiresAt": end, "registrar": "Example GmbH", "failed": false},
		"shop.net": {"name": "shop.net", "status": "registered", "note": "the net zone delegates it", "reason": "no_answer", "failed": false},
		"shop.al":  {"name": "shop.al", "status": "unknown", "note": "the registry of .al publishes no WHOIS or RDAP service", "reason": "no_lookup", "failed": false},
	}
	if reg, err := f.m.lookupMore(t.Context(), "shop.de", errNoEnd); err != nil || !reg.expires.Equal(end) || reg.registrar != "Example GmbH" {
		t.Errorf("shop.de = %+v %v", reg, err)
	}
	if _, err := f.m.lookupMore(t.Context(), "shop.net", errNoRDAP); !errors.Is(err, errNoEnd) || err.Error() != "the net zone delegates it" || causeOf(err) != causeNoAnswer {
		t.Errorf("shop.net = %v", err)
	}
	if _, err := f.m.lookupMore(t.Context(), "shop.al", errNoRDAP); causeOf(err) != causeNoLookup {
		t.Errorf("shop.al = %v, cause %q", err, causeOf(err))
	}
	if causeOf(errNoRDAP) != causeNoLookup || causeOf(errNoEnd) != causeNoEnd || causeOf(errors.New("timeout")) != "" {
		t.Error("causes of the lookup's own errors")
	}
	failed := errors.New("WHOIS whois.nic.org.uy: connection refused")
	if _, err := f.m.lookupMore(t.Context(), "shop.uy", failed); !errors.Is(err, failed) {
		t.Errorf("whoice off = %v", err)
	}
	f.more["shop.uy"] = map[string]any{"name": "shop.uy", "status": "unknown", "note": "timeout", "failed": true}
	if _, err := f.m.lookupMore(t.Context(), "shop.uy", failed); !errors.Is(err, failed) {
		t.Errorf("whoice failed = %v", err)
	}

	// IANA names no WHOIS server for .al: no RDAP, rather than a server
	// named "status:".
	f.m.rdap.whois = func(_ context.Context, server, query string) (string, error) {
		if server == "whois.iana.org" && query == "al" {
			// IANA leaves the line empty for a TLD without WHOIS.
			return "domain:       AL\nwhois:        \n\nstatus:       ACTIVE\n", nil
		}
		return fakeWhois(t.Context(), server, query)
	}
	if _, err := f.m.rdap.lookup(t.Context(), "shop.al"); !errors.Is(err, errNoRDAP) {
		t.Errorf("shop.al without WHOIS = %v", err)
	}
}

func TestParseWhois(t *testing.T) {
	for answer, want := range map[string]string{
		"Registry Expiry Date: 2027-09-30T01:00:00Z\nRegistrar: MarkMonitor Inc.":          "2027-09-30 MarkMonitor Inc.",
		"Expiration Time: 2029-03-17 12:48:36\nSponsoring Registrar: X":                    "2029-03-17 X",
		"    Registrar:\n        Example Ltd\n    Expiry date:  13-Dec-2034\n":             "2034-12-13 Example Ltd",
		"[Domain Name]  EXAMPLE.JP\n[Expires on]                    2026/09/30\n":          "2026-09-30 ",
		"Registrar Registration Expiration Date: 2027-01-02T00:00:00.000Z\nRegistrar: R\n": "2027-01-02 R",
	} {
		reg, err := parseWhois(answer)
		if got := reg.expires.Format(time.DateOnly) + " " + reg.registrar; err != nil || got != want {
			t.Errorf("parseWhois(%q) = %q %v, want %q", answer, got, err, want)
		}
	}
	if _, err := parseWhois("Status: connect"); !errors.Is(err, errNoEnd) {
		t.Errorf("no end: %v", err)
	}
}

// The domains looked up before the cause was kept have it from the words
// of their error.
func TestDomainCauseMigration(t *testing.T) {
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(t.Context(), "watch", firstMigrations(t, 5)); err != nil {
		t.Fatal(err)
	}
	db := st.DB()
	for name, row := range map[string][2]string{
		"nn.tn":    {stateUnknown, "the registry tells no expiration date"},
		"shop.al":  {stateUnknown, "the registry of .al publishes no WHOIS or RDAP service"},
		"shop.uy":  {stateUnknown, "the WHOIS of .uy cannot be asked from here, and it has no RDAP"},
		"shop.net": {stateUnknown, "the registry of this TLD has no RDAP service"},
		"gone.dz":  {stateUnknown, "WHOIS whois.nic.dz: timeout; the dz zone has no such name"},
		"shop.com": {stateFailed, "rdap: 503 Service Unavailable"},
	} {
		if _, err := db.Exec(`INSERT INTO watch_domains (name, manual, certificates, sites, created_at, check_at, state, registrar, error, notified)
			VALUES (?, 1, '[]', '[]', 0, 0, ?, '', ?, '')`, name, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Migrate(t.Context(), "watch", New().Migrations()); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT name, cause FROM watch_domains ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, cause string
		if err := rows.Scan(&name, &cause); err != nil {
			t.Fatal(err)
		}
		got = append(got, name+" "+cause)
	}
	want := []string{"gone.dz no_name", "nn.tn no_end", "shop.al no_lookup", "shop.com ", "shop.net no_lookup", "shop.uy no_lookup"}
	if !slices.Equal(got, want) {
		t.Fatalf("after the migration %v, want %v", got, want)
	}
}

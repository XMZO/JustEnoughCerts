package monitor

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

var start = time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC)

type fixture struct {
	t   *testing.T
	m   *Module
	api humatest.TestAPI
	ca  *cryptoxtest.CA
	now time.Time

	mu      sync.Mutex
	served  map[string]*x509.Certificate // by domain, or by the address of a view; none is unreachable
	targets map[string][2]string         // where the DNS of a domain sends mainland China and elsewhere
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
	if _, err := st.Migrate(t.Context(), "monitor", New().Migrations()); err != nil {
		t.Fatal(err)
	}
	bus := events.New(st.DB(), slog.New(slog.DiscardHandler), func(string) bool { return true })
	bus.Seal()
	f := &fixture{t: t, ca: cryptoxtest.NewCA(t), now: start, served: map[string]*x509.Certificate{}, targets: map[string][2]string{}}
	f.m = &Module{now: func() time.Time { return f.now }, db: st.DB(), events: bus.Scope("monitor"), log: slog.New(slog.DiscardHandler)}
	f.m.serve = func(_ context.Context, domain, address string) (*x509.Certificate, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if address == "" {
			address = domain
		}
		if c := f.served[address]; c != nil {
			return c, nil
		}
		return nil, errors.New("dial tcp: connection refused")
	}
	// A DNS that does not answer by region, unless split says otherwise.
	f.m.target = func(_ context.Context, name string, subnet netip.Prefix) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		t, ok := f.targets[name]
		switch {
		case !ok:
			return "", errors.New("no answer")
		case subnet == dnscheck.MainlandChina:
			return t[0], nil
		}
		return t[1], nil
	}
	_, f.api = humatest.New(t)
	registerAPI(f.api, f.m)
	return f
}

// leaf is a certificate for names that ends after days.
func (f *fixture) leaf(days int, names ...string) *x509.Certificate {
	return f.ca.Leaf(f.t, cryptoxtest.NewKey(f.t), f.now.Add(-time.Hour), f.now.Add(time.Duration(days)*24*time.Hour), names...)
}

// split has the DNS of domain send mainland China to china and elsewhere to
// global.
func (f *fixture) split(domain, china, global string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.targets[domain] = [2]string{china, global}
}

// foreign is a certificate for names from a CA of org, not the test CA, that
// ends after days: as the own certificate of a CDN.
func (f *fixture) foreign(org string, days int, names ...string) *x509.Certificate {
	f.t.Helper()
	key := cryptoxtest.NewKey(f.t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: names[0], Organization: []string{org}}, DNSNames: names,
		NotBefore: f.now.Add(-time.Hour), NotAfter: f.now.Add(time.Duration(days) * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		f.t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

func (f *fixture) serve(domain string, c *x509.Certificate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.served[domain] = c
}

// tell hands the module an event of the certs module about certificate 1.
func (f *fixture) tell(typ string, version int64, c *x509.Certificate) {
	f.t.Helper()
	d := contracts.CertificateEvent{CertificateID: 1, Domains: []string{"example.com", "www.example.com", "*.example.com"}}
	if c != nil {
		d.VersionID, d.Fingerprint, d.NotAfter = version, cryptox.Fingerprint(c), c.NotAfter
	}
	data, err := json.Marshal(d)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.m.handle(f.t.Context(), events.Event{Type: typ, Subject: "1", Data: data, Time: f.now}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) certificate() CertificateChecksBody {
	f.t.Helper()
	resp := f.api.Get("/certificates/1")
	var body CertificateChecksBody
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &body) != nil {
		f.t.Fatalf("GET = %d %s", resp.Code, resp.Body.String())
	}
	return body
}

func (f *fixture) checkAt() time.Time {
	f.t.Helper()
	b := f.certificate()
	if b.CheckAt == nil {
		f.t.Fatal("not tracked")
	}
	return *b.CheckAt
}

// alerts are the CertificateServedOld events published, by domain.
func (f *fixture) alerts() []string {
	f.t.Helper()
	rows, err := f.m.db.QueryContext(f.t.Context(), "SELECT data FROM kernel_events WHERE type = ? ORDER BY id", contracts.CertificateServedOld)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var data string
		var d contracts.CertificateEvent
		if err := rows.Scan(&data); err != nil || json.Unmarshal([]byte(data), &d) != nil || d.Check == nil {
			f.t.Fatalf("event %s: %v", data, err)
		}
		out = append(out, d.Check.Domain)
	}
	return out
}

func TestTrack(t *testing.T) {
	f := newFixture(t)
	if b := f.certificate(); b.Tracked {
		t.Fatalf("tracked before any event: %+v", b)
	}
	v1 := f.leaf(90, "example.com", "www.example.com", "*.example.com")
	f.tell(contracts.CertificateIssued, 1, v1)
	if got := f.checkAt(); !got.Equal(start.Add(afterIssue)) {
		t.Errorf("a new version is checked at %s", got)
	}
	// Told again, it keeps its schedule.
	f.now = start.Add(time.Minute)
	f.tell(contracts.CertificateCurrent, 1, v1)
	if got := f.checkAt(); !got.Equal(start.Add(afterIssue)) {
		t.Errorf("told again: %s", got)
	}
	// Deployed: soon. Another version deployed changes nothing.
	f.tell(contracts.CertificateDeployed, 9, f.leaf(30, "example.com"))
	if got := f.checkAt(); !got.Equal(start.Add(afterIssue)) {
		t.Errorf("another version deployed: %s", got)
	}
	f.tell(contracts.CertificateDeployed, 1, v1)
	if got := f.checkAt(); !got.Equal(f.now.Add(afterDeploy)) {
		t.Errorf("deployed: %s", got)
	}
	// What the certs module tells of at a module's request is checked at once.
	f.tell(contracts.CertificateCurrent, 2, f.leaf(120, "example.com"))
	if got := f.checkAt(); !got.Equal(f.now) {
		t.Errorf("announced: %s", got)
	}
	f.tell(contracts.CertificateDeleted, 0, nil)
	if b := f.certificate(); b.Tracked {
		t.Errorf("deleted, still %+v", b)
	}
}

func TestCheck(t *testing.T) {
	f := newFixture(t)
	old := f.leaf(20, "example.com", "www.example.com")
	v1 := f.leaf(90, "example.com", "www.example.com", "*.example.com")
	f.tell(contracts.CertificateIssued, 1, v1)
	f.serve("example.com", v1)
	f.serve("www.example.com", old) // the CDN in front of www did not take it

	// Deployed a minute later: checked at once, but the grace is not over.
	f.now = start.Add(5 * time.Minute)
	if err := f.m.checkDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.tell(contracts.CertificateDeployed, 1, v1)
	f.now = f.now.Add(afterDeploy)
	if err := f.m.checkDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	b := f.certificate()
	if len(b.Results) != 2 || b.Results[0].Domain != "example.com" || b.Results[0].State != stateOK ||
		b.Results[1].State != stateOld || b.Results[1].NotAfter == nil || !b.Results[1].NotAfter.Equal(old.NotAfter) {
		t.Fatalf("results %+v", b.Results)
	}
	if len(b.Skipped) != 1 || b.Skipped[0] != "*.example.com" {
		t.Errorf("skipped %v", b.Skipped)
	}
	if len(f.alerts()) != 0 {
		t.Error("told of it before the grace was over")
	}
	// Checked again once the grace is over, and told of once.
	if got := *b.CheckAt; !got.Equal(start.Add(grace)) {
		t.Errorf("next check %s, want the end of the grace", got)
	}
	f.now = start.Add(grace)
	if err := f.m.checkDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(again)
	if err := f.m.checkDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.alerts(); len(got) != 1 || got[0] != "www.example.com" {
		t.Fatalf("alerts %v", got)
	}

	// Fixed: back to the usual pace. Old again later: told of again.
	f.serve("www.example.com", v1)
	f.now = f.now.Add(again)
	if err := f.m.checkDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.checkAt(); !got.Equal(f.now.Add(every)) {
		t.Errorf("next check when fine: %s", got)
	}
	f.serve("www.example.com", old)
	f.now = f.now.Add(every)
	if err := f.m.checkDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.alerts(); len(got) != 2 {
		t.Errorf("alerts after it broke again: %v", got)
	}
}

func TestStates(t *testing.T) {
	f := newFixture(t)
	v1 := f.leaf(90, "example.com", "www.example.com")
	f.tell(contracts.CertificateIssued, 1, v1)
	f.serve("example.com", f.leaf(90, "other.example")) // names of another site
	resp := f.api.Post("/certificates/1/check")
	var b CertificateChecksBody
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &b) != nil {
		t.Fatalf("check = %d %s", resp.Code, resp.Body.String())
	}
	if len(b.Results) != 2 || b.Results[0].State != stateOther || b.Results[1].State != stateUnreachable || b.Results[1].Error == "" {
		t.Errorf("results %+v", b.Results)
	}
	resp = f.api.Get("/states")
	if resp.Code != http.StatusOK || resp.Body.String() != `{"certificates":[{"certificateId":1,"old":0,"unreachable":1}]}`+"\n" {
		t.Errorf("states = %d %s", resp.Code, resp.Body.String())
	}
	if resp := f.api.Post("/certificates/" + strconv.Itoa(2) + "/check"); resp.Code != http.StatusNotFound {
		t.Errorf("an unknown one = %d", resp.Code)
	}
}

func TestServedAt(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	leaf, err := servedAt(t.Context(), "example.com", srv.Listener.Addr().String())
	if err != nil || !leaf.Equal(srv.Certificate()) {
		t.Errorf("served %v, %v", leaf, err)
	}
	if _, err := servedAt(t.Context(), "example.com", "127.0.0.1:1"); err == nil {
		t.Error("nothing listening, and no error")
	}
}

// A domain whose DNS sends mainland China to the CDN JEC deploys to and
// elsewhere to another CDN: the other CDN's own certificate is shown, not
// told of; an older version on one side is told of, for that side; the two
// sides alike again make one line.
func TestViews(t *testing.T) {
	f := newFixture(t)
	f.split("example.com", "cn.cdn.example", "global.cdn.example")
	v1 := f.leaf(90, "example.com", "www.example.com", "*.example.com")
	f.tell(contracts.CertificateIssued, 1, v1)
	f.serve("cn.cdn.example", v1)
	f.serve("global.cdn.example", f.foreign("Elsewhere", 30, "example.com"))
	f.serve("www.example.com", v1)

	f.now = start.Add(grace)
	if _, err := f.m.check(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	b := f.certificate()
	if len(b.Results) != 3 || b.Results[0].View != viewChina || b.Results[0].State != stateOK ||
		b.Results[1].View != viewGlobal || b.Results[1].State != stateOther || b.Results[1].Issuer != "example.com" ||
		b.Results[2].Domain != "www.example.com" || b.Results[2].View != viewAll || b.Results[2].State != stateOK {
		t.Fatalf("results %+v", b.Results)
	}
	if len(f.alerts()) != 0 {
		t.Fatal("told of the other CDN's certificate")
	}

	// Mainland China falls back to an older version: told of, for it.
	f.serve("cn.cdn.example", f.leaf(20, "example.com"))
	if _, err := f.m.check(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if b := f.certificate(); b.Results[0].State != stateOld || b.Results[1].State != stateOther {
		t.Fatalf("results %+v", b.Results)
	}
	rows, err := f.m.db.QueryContext(t.Context(), "SELECT data FROM kernel_events WHERE type = ?", contracts.CertificateServedOld)
	if err != nil {
		t.Fatal(err)
	}
	var views []string
	for rows.Next() {
		var data string
		var d contracts.CertificateEvent
		if rows.Scan(&data) != nil || json.Unmarshal([]byte(data), &d) != nil {
			t.Fatal(data)
		}
		views = append(views, d.Check.Domain+" "+d.Check.View)
	}
	_ = rows.Close()
	if len(views) != 1 || views[0] != "example.com cn" {
		t.Errorf("alerts %v", views)
	}

	// Both sides serve the version in use: one line again.
	f.serve("cn.cdn.example", v1)
	f.serve("global.cdn.example", v1)
	if _, err := f.m.check(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if b := f.certificate(); len(b.Results) != 2 || b.Results[0].View != viewAll || b.Results[0].State != stateOK {
		t.Errorf("results %+v", b.Results)
	}
}

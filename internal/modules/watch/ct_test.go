package watch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
)

// logs is a fake of Cert Spotter: the certificates of each domain, in the
// order they were found, told two to a page.
type logs struct {
	mu      sync.Mutex
	certs   map[string][]spotted
	limited bool
	asked   []string // domain and after of each search
	auth    []string
}

func (l *logs) add(domain string, s spotted) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.certs[domain] = append(l.certs[domain], s)
}

func (l *logs) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		q := r.URL.Query()
		if r.URL.Path != "/v1/issuances" || q.Get("include_subdomains") != "true" || !slices.Equal(q["expand"], []string{"dns_names", "issuer"}) {
			t.Errorf("asked %s", r.URL)
		}
		l.asked = append(l.asked, q.Get("domain")+" "+q.Get("after"))
		l.auth = append(l.auth, r.Header.Get("Authorization"))
		if l.limited {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		var page []spotted
		for _, s := range l.certs[q.Get("domain")] {
			if q.Get("after") == "" || s.ID > q.Get("after") {
				page = append(page, s)
			}
		}
		if len(page) > 2 {
			page = page[:2]
		}
		if page == nil {
			page = []spotted{}
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// logged is a certificate as the fake logs tell it.
func logged(id, key, issuer string, from time.Time, names ...string) spotted {
	s := spotted{ID: id, SHA256: strings.Repeat(id[len(id)-1:], 64), DNSNames: names, Pubkey: key, NotBefore: from, NotAfter: from.Add(90 * 24 * time.Hour)}
	s.Issuer.FriendlyName, s.Issuer.Name = issuer, "C=US, O="+issuer+", CN=R1"
	return s
}

func newCTFixture(t *testing.T) (*fixture, *logs, *[]certificate, *map[string]int64) {
	f := newFixture(t)
	box, _, err := secrets.Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	f.m.secrets = box.Scope("watch")
	l := &logs{certs: map[string][]spotted{}}
	srv := l.serve(t)
	f.m.spotter.client, f.m.spotter.url = srv.Client(), srv.URL+"/v1/issuances"
	certs := &[]certificate{}
	keys := &map[string]int64{}
	f.m.call = func(_ context.Context, via, method, path string, _, out any) error {
		if via != "watch" || method != http.MethodGet {
			t.Errorf("asked %s %s as %s", method, path, via)
		}
		var data []byte
		switch path {
		case "/certs/certificates":
			data, _ = json.Marshal(map[string]any{"certificates": *certs})
		case "/certs/public-keys":
			var list []map[string]any
			for k, id := range *keys {
				list = append(list, map[string]any{"certificateId": id, "versionId": 1, "sha256": k})
			}
			data, _ = json.Marshal(map[string]any{"keys": list})
		default:
			return &module.CallError{Status: http.StatusNotFound}
		}
		return json.Unmarshal(data, out)
	}
	return f, l, certs, keys
}

func (f *fixture) runCT() {
	f.t.Helper()
	if err := f.m.transparencyDue(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) ct() TransparencyBody {
	f.t.Helper()
	out, err := f.m.transparency(f.t.Context())
	if err != nil {
		f.t.Fatal(err)
	}
	return out.Body
}

func (f *fixture) spottedTold() []contracts.SpottedEvent {
	f.t.Helper()
	rows, err := f.m.db.QueryContext(f.t.Context(), "SELECT data FROM kernel_events WHERE type = ? ORDER BY id", contracts.CertificateSpotted)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []contracts.SpottedEvent
	for rows.Next() {
		var data string
		var e contracts.SpottedEvent
		if err := rows.Scan(&data); err != nil || json.Unmarshal([]byte(data), &e) != nil {
			f.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// listed returns the IDs and states of the certificates listed.
func listed(b TransparencyBody) []string {
	var out []string
	for _, c := range b.Certificates {
		out = append(out, c.ID+" "+c.State)
	}
	return out
}

func TestTransparency(t *testing.T) {
	f, l, certs, keys := newCTFixture(t)
	*certs = []certificate{{ID: 1, Domains: []string{"shop.com", "www.shop.com"}}}
	(*keys)["aaaa"] = 1
	l.add("shop.com", logged("100", "AAAA", "Let's Encrypt", start.Add(-30*24*time.Hour), "www.shop.com", "shop.com"))
	l.add("shop.com", logged("101", "cdn1", "Google Trust Services", start.Add(-20*24*time.Hour), "shop.com", "*.shop.com"))
	l.add("shop.com", logged("102", "old", "Sectigo", start.Add(-100*24*time.Hour), "shop.com")) // ended

	// Off, nothing is searched.
	f.runCT()
	if len(l.asked) != 0 {
		t.Fatalf("searched while off: %v", l.asked)
	}
	if resp := f.api.Put("/transparency", map[string]any{"enabled": true}); resp.Code != http.StatusOK {
		t.Fatalf("switch on = %d %s", resp.Code, resp.Body)
	}

	// The first search goes to the end, two to a page: JEC's own is
	// recognized by its key, the other is listed but not told of.
	f.runCT()
	if want := []string{"shop.com ", "shop.com 101", "shop.com 102"}; !slices.Equal(l.asked, want) {
		t.Fatalf("searched %v, want %v", l.asked, want)
	}
	b := f.ct()
	if !b.Enabled || b.HasKey || b.Own != 1 || len(b.Domains) != 1 || !b.Domains[0].Scanned || b.Domains[0].Error != "" {
		t.Fatalf("after the first search: %+v", b)
	}
	if got := listed(b); !slices.Equal(got, []string{"101 baseline"}) {
		t.Fatalf("listed %v", got)
	}
	if c := b.Certificates[0]; !slices.Equal(c.Names, []string{"*.shop.com", "shop.com"}) || c.Issuer != "Google Trust Services" || c.Domain != "shop.com" {
		t.Fatalf("listed %+v", c)
	}

	// Not due yet: no search.
	l.asked = nil
	f.runCT()
	if len(l.asked) != 0 {
		t.Fatalf("searched before due: %v", l.asked)
	}

	// Later: one nobody knows, and JEC's next, stored a while after it
	// was logged. Neither is told of at once.
	l.add("shop.com", logged("103", "evil", "Some CA", start, "www.shop.com"))
	l.add("shop.com", logged("104", "bbbb", "Let's Encrypt", start, "www.shop.com", "shop.com"))
	f.now = f.now.Add(ctEvery)
	f.runCT()
	if got := listed(f.ct()); !slices.Equal(got, []string{"103 new", "104 new", "101 baseline"}) {
		t.Fatalf("listed %v", got)
	}
	if told := f.spottedTold(); len(told) != 0 {
		t.Fatalf("told at once: %+v", told)
	}
	(*keys)["bbbb"] = 1
	f.now = f.now.Add(ctGrace)
	f.runCT()
	b = f.ct()
	if got := listed(b); !slices.Equal(got, []string{"103 unknown", "101 baseline"}) || b.Own != 2 {
		t.Fatalf("listed %v, own %d", got, b.Own)
	}
	told := f.spottedTold()
	if len(told) != 1 || told[0].Domain != "shop.com" || !slices.Equal(told[0].Names, []string{"www.shop.com"}) || told[0].Issuer != "Some CA" ||
		told[0].SHA256 != strings.Repeat("3", 64) || !told[0].NotBefore.Equal(start) {
		t.Fatalf("told %+v", told)
	}
	f.runCT() // told once
	if n := len(f.spottedTold()); n != 1 {
		t.Fatalf("told %d times", n)
	}

	// The CDN's names and issuer trusted: its renewal is not told of.
	if resp := f.api.Post("/transparency/certificates/101/trust"); resp.Code != http.StatusNoContent {
		t.Fatalf("trust = %d %s", resp.Code, resp.Body)
	}
	l.add("shop.com", logged("105", "cdn2", "Google Trust Services", f.now, "*.shop.com", "shop.com"))
	f.now = f.now.Add(ctEvery + ctGrace)
	f.runCT()
	f.now = f.now.Add(ctGrace)
	f.runCT()
	b = f.ct()
	if got := listed(b); !slices.Equal(got, []string{"103 unknown"}) || len(b.Trusted) != 1 || b.Trusted[0].Count != 2 ||
		!slices.Equal(b.Trusted[0].Names, []string{"*.shop.com", "shop.com"}) || b.Trusted[0].Issuer != "Google Trust Services" {
		t.Fatalf("after trusting: %v %+v", got, b.Trusted)
	}
	if n := len(f.spottedTold()); n != 1 {
		t.Fatalf("told %d times", n)
	}

	// Ignored, it is no longer listed.
	if resp := f.api.Post("/transparency/certificates/103/ignore"); resp.Code != http.StatusNoContent {
		t.Fatalf("ignore = %d %s", resp.Code, resp.Body)
	}
	if resp := f.api.Post("/transparency/certificates/103/ignore"); resp.Code != http.StatusNotFound {
		t.Fatalf("ignore again = %d", resp.Code)
	}
	if b := f.ct(); len(b.Certificates) != 0 || b.Ignored != 1 {
		t.Fatalf("after ignoring: %+v", b)
	}

	// The trust taken back: what it covered is listed again, not told of.
	resp := f.api.Post("/transparency/untrust", map[string]any{"names": []string{"shop.com", "*.shop.com"}, "issuer": "Google Trust Services"})
	if resp.Code != http.StatusNoContent {
		t.Fatalf("untrust = %d %s", resp.Code, resp.Body)
	}
	if b := f.ct(); !slices.Equal(listed(b), []string{"105 baseline", "101 baseline"}) || len(b.Trusted) != 0 {
		t.Fatalf("after untrusting: %v %+v", listed(b), b.Trusted)
	}

	// An API key goes with the searches; too many requests wait for later.
	l.limited, l.auth = true, nil
	if resp := f.api.Put("/transparency", map[string]any{"enabled": true, "apiKey": " k1 "}); resp.Code != http.StatusOK {
		t.Fatalf("save key = %d %s", resp.Code, resp.Body)
	}
	if resp := f.api.Post("/transparency/check"); resp.Code != http.StatusAccepted {
		t.Fatalf("check = %d %s", resp.Code, resp.Body)
	}
	f.runCT()
	b = f.ct()
	if !b.HasKey || !slices.Equal(l.auth, []string{"Bearer k1"}) || !strings.Contains(b.Domains[0].Error, "too many requests") {
		t.Fatalf("limited: %+v, auth %v", b, l.auth)
	}
	if resp := f.api.Put("/transparency", map[string]any{"enabled": true}); resp.Code != http.StatusOK || !f.ct().HasKey {
		t.Fatalf("save without the key = %d, the key gone", resp.Code)
	}
	if resp := f.api.Put("/transparency", map[string]any{"enabled": true, "apiKey": ""}); resp.Code != http.StatusOK || f.ct().HasKey {
		t.Fatalf("clear the key = %d, the key kept", resp.Code)
	}

	// A certificate that ended a while ago is forgotten; a domain JEC no
	// longer holds certificates under goes with what was found for it.
	l.limited = false
	f.now = start.Add(100 * 24 * time.Hour)
	f.runCT()
	var n int
	if err := f.m.db.QueryRow("SELECT count(*) FROM watch_ct_certificates WHERE id IN ('100', '101')").Scan(&n); err != nil || n != 0 {
		t.Fatalf("ended kept: %d %v", n, err)
	}
	*certs = nil
	f.runCT()
	if b := f.ct(); len(b.Domains) != 0 || len(b.Certificates) != 0 || b.Own != 0 {
		t.Fatalf("after the certificate went: %+v", b)
	}
}

package lookup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/parse"
)

// registries fakes IANA's bootstrap file, which names an RDAP server for
// com only, the RDAP servers of com and (from the extra list) de, and the
// web lookup of .lk.
func registries(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dns.json":
			_, _ = fmt.Fprintf(w, `{"services": [[["com"], [%q]]]}`, srv.URL+"/com/")
		case "/com/domain/shop.com":
			_, _ = w.Write([]byte(`{"objectClassName": "domain", "ldhName": "shop.com", "events": [
				{"eventAction": "registration", "eventDate": "2001-02-03T04:05:06Z"},
				{"eventAction": "expiration", "eventDate": "2030-02-03T04:05:06Z"}],
				"entities": [{"roles": ["registrar"], "vcardArray": ["vcard", [["fn", {}, "text", "Example Registrar"]]]}]}`))
		case "/com/domain/gone.com":
			http.NotFound(w, r)
		case "/de/domain/shop.de":
			// DENIC tells no end.
			_, _ = w.Write([]byte(`{"objectClassName": "domain", "ldhName": "shop.de", "events": [{"eventAction": "last changed", "eventDate": "2024-01-01T00:00:00Z"}]}`))
		case "/lk/proxy/domains/single-search":
			if r.URL.Query().Get("keyword") != "shop.lk" {
				t.Errorf("lk asked %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"result": {"domainAvailability": {"message": "Domain name you searched is already registered", "domainName": "shop.lk",
				"domainInfo": {"expireDate": "Friday, 3rd October, 2031", "registeredTo": "Shop"}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeWhois answers as CNNIC does, and as DENIC does to the form of query
// it wants; whois.nic.down does not answer.
func fakeWhois(t *testing.T) func(context.Context, string, string) (string, error) {
	return func(_ context.Context, server, query string) (string, error) {
		switch server + " " + query {
		case "whois.cnnic.cn shop.cn":
			return "Domain Name: shop.cn\nSponsoring Registrar: Example Registrar\nExpiration Time: 2029-03-17 12:48:36\n", nil
		case "whois.denic.de -T dn shop.de":
			return "Domain: shop.de\nNserver: ns1.example.net\nStatus: connect\nChanged: 2024-01-01T00:00:00+01:00\n", nil
		}
		t.Logf("WHOIS %s asked %q", server, query)
		return "", errors.New("connection refused")
	}
}

func newTestLookup(t *testing.T, d delegation) *Lookup {
	srv := registries(t)
	l := &Lookup{
		rdap:         &rdap{client: srv.Client(), bootstrap: srv.URL + "/dns.json", extra: map[string]string{"de": srv.URL + "/de/"}, now: time.Now},
		whoisServers: loadWhoisServers(),
		whoisQuery:   fakeWhois(t),
		webClient:    srv.Client(),
		delegation:   d,
		parsers:      parse.Default(),
	}
	for _, m := range webModules {
		if m.name == "lk" {
			m.base = srv.URL + "/lk"
		}
		l.webModules = append(l.webModules, m)
	}
	return l
}

func TestDomain(t *testing.T) {
	zones := map[string][]string{"uy": {"a.nic.uy"}, "al": {"nsx.nic.al"}}
	replies := map[string]zoneReply{
		"a.nic.uy":   {nsOwner: "ka.uy", nameservers: []string{"ns1.example.net"}},
		"nsx.nic.al": {nsOwner: "dig.al", nameservers: []string{"ns1.example.net"}},
	}
	l := newTestLookup(t, fakeDelegation(t, zones, replies))
	at := func(s string) time.Time {
		v, err := time.Parse(time.DateTime, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tt := range []struct {
		name, in  string
		status    model.ResultStatus
		expires   time.Time
		registrar string
		source    model.SourceName
		note      string
	}{
		{name: "RDAP", in: "www.Shop.COM.", status: model.StatusRegistered, expires: at("2030-02-03 04:05:06"), registrar: "Example Registrar", source: model.SourceRDAP},
		{name: "RDAP: no such name", in: "gone.com", status: model.StatusUnregistered, source: model.SourceRDAP, note: "the registry has no such name"},
		// CNNIC's times are China's.
		{name: "WHOIS, read by the .cn parser", in: "shop.cn", status: model.StatusRegistered, expires: at("2029-03-17 04:48:36"), registrar: "Example Registrar", source: model.SourceWHOIS},
		{name: "DENIC: its form of query, and no end", in: "shop.de", status: model.StatusRegistered, source: model.SourceRDAP, note: "the registry tells no expiration date"},
		{name: "a registry's web lookup", in: "shop.lk", status: model.StatusRegistered, expires: at("2031-10-03 00:00:00"), source: model.SourceWHOISWeb},
		{name: "a WHOIS that cannot be asked: the delegation", in: "ka.uy", status: model.StatusRegistered, source: model.SourceDNS,
			note: "the WHOIS of .uy cannot be asked from here, and it has no RDAP; the uy zone delegates it"},
		{name: "no lookup at all: the delegation", in: "dig.al", status: model.StatusRegistered, source: model.SourceDNS,
			note: "the registry of .al publishes no WHOIS or RDAP service; the al zone delegates it"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, err := l.Domain(t.Context(), tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != tt.status || !r.Expires.Equal(tt.expires) || r.Registrar != tt.registrar || r.Source != tt.source || r.Note != tt.note || r.Failed {
				t.Errorf("got %+v", r)
			}
		})
	}
}

// TestDomainFailed: a registry that does not answer, where the delegation
// cannot be had either, is a failure worth trying again; one with no
// lookup at all and no delegation is an error.
func TestDomainFailed(t *testing.T) {
	l := newTestLookup(t, fakeDelegation(t, map[string][]string{"cn": {"dead.cnnic.cn"}}, nil))
	r, err := l.Domain(t.Context(), "down.cn")
	if err != nil || !r.Failed || r.Status != model.StatusUnknown || !strings.Contains(r.Note, "WHOIS whois.cnnic.cn: connection refused") {
		t.Errorf("down.cn = %+v, %v", r, err)
	}
	if _, err := l.Domain(t.Context(), "dig.al"); err == nil || !strings.Contains(err.Error(), "publishes no WHOIS or RDAP service") {
		t.Errorf("dig.al = %v", err)
	}
	if _, err := l.Domain(t.Context(), "co.uk"); !errors.Is(err, ErrDomain) {
		t.Errorf("co.uk = %v", err)
	}
}

// TestPrivateSuffix: under a private suffix the TLD's servers are not
// asked, as its registry holds none of its names.
func TestPrivateSuffix(t *testing.T) {
	q, err := normalize("shop.github.io")
	if err != nil || !q.PrivateSuffix || q.Suffix != "github.io" || q.Query != "shop.github.io" {
		t.Fatalf("normalize = %+v, %v", q, err)
	}
	l := newTestLookup(t, delegation{})
	if _, _, ok := l.whoisServers.resolve(q); ok {
		t.Error("WHOIS of .io asked for shop.github.io")
	}
	l.rdap.extra["io"] = "https://rdap.example/"
	if _, err := l.rdap.lookup(t.Context(), q); !errors.Is(err, errSkip) {
		t.Errorf("RDAP of .io asked for shop.github.io: %v", err)
	}
}

func TestWhoisServers(t *testing.T) {
	s := loadWhoisServers()
	for in, want := range map[string][2]string{
		"shop.de":    {"whois.denic.de", "-T dn shop.de"},
		"shop.com":   {"whois.verisign-grs.com", "=shop.com"},
		"shop.jp":    {"whois.jprs.jp", "shop.jp/e"},
		"shop.co.uk": {"whois.nic.uk", "shop.co.uk"},
	} {
		q, err := normalize(in)
		if err != nil {
			t.Fatal(err)
		}
		server, query, ok := s.resolve(q)
		if !ok || server.host != want[0] || query != want[1] {
			t.Errorf("%s: %s %q", in, server.host, query)
		}
	}
	if q, _ := normalize("dig.al"); func() bool { _, _, ok := s.resolve(q); return ok }() {
		t.Error("a WHOIS server for .al")
	}
}

func TestRefused(t *testing.T) {
	if got := refused("% Too many queries from your IP, try again later\n"); got == "" {
		t.Error("a refusal read as a record")
	}
	if got := refused("Domain Name: shop.mu\nTerms: too many queries may be limited ...\n"); got != "" {
		t.Errorf("a record read as a refusal: %q", got)
	}
}

func TestDecode(t *testing.T) {
	if got := decode([]byte{'M', 0xfc, 'n', 'c', 'h', 'e', 'n'}); got != "München" {
		t.Errorf("decode = %q", got)
	}
}

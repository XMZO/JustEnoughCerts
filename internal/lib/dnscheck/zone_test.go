package dnscheck

import (
	"context"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/miekg/dns"
)

// askedAuthorities makes servers the authoritative servers of every zone,
// for one test, and records the names they were looked up for.
func askedAuthorities(t *testing.T, servers ...string) *[]string {
	t.Helper()
	saved := authorities
	t.Cleanup(func() { authorities = saved })
	asked := &[]string{}
	authorities = func(_ context.Context, name string) ([]string, error) {
		*asked = append(*asked, name)
		return servers, nil
	}
	return asked
}

// The zone of a record comes from its parent: no resolver is asked about
// the record's own name, which does not exist yet.
func TestRecordServers(t *testing.T) {
	soa, _ := dns.NewRR("example.com. 3600 IN SOA ns1.example.com. admin.example.com. 1 3600 180 1209600 180")
	addr := serveDNS(t, map[string]dns.RR{"example.com.": soa})
	asked := askedAuthorities(t, addr)
	servers, err := recordServers(t.Context(), "_acme-challenge.WWW.example.com.")
	if err != nil || !slices.Equal(servers, []string{addr}) {
		t.Fatalf("servers %v, %v", servers, err)
	}
	if !slices.Equal(*asked, []string{"www.example.com"}) {
		t.Errorf("looked up %v", *asked)
	}
}

// serveCut answers as the servers of a zone that delegates
// _acme-challenge.cut.test. to servers of its own.
func serveCut(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, m *dns.Msg) {
		out := new(dns.Msg)
		out.SetReply(m)
		if name := strings.ToLower(m.Question[0].Name); name == "_acme-challenge.cut.test." {
			out.Ns = append(out.Ns, &dns.NS{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 300}, Ns: "ns.acme.test."})
		} else {
			out.Authoritative = true
			out.Rcode = dns.RcodeNameError
		}
		_ = w.WriteMsg(out)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func TestZoneCut(t *testing.T) {
	addr := serveCut(t)
	if !zoneCut(t.Context(), "_acme-challenge.cut.test", []string{addr}) {
		t.Error("a delegated name is no zone cut")
	}
	if zoneCut(t.Context(), "_acme-challenge.plain.test", []string{addr}) {
		t.Error("a name in the zone is a zone cut")
	}
	// A delegated name is looked up itself, as a zone exists there.
	asked := askedAuthorities(t, addr)
	if _, err := recordServers(t.Context(), "_acme-challenge.cut.test"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*asked, []string{"cut.test", "_acme-challenge.cut.test"}) {
		t.Errorf("looked up %v", *asked)
	}
}

// After PrimeZone, lego finds the zone in its cache: the zone here is no
// real one, so a resolver would not know it.
func TestPrimeZone(t *testing.T) {
	soa, _ := dns.NewRR("jec-prime.test. 3600 IN SOA ns1.jec-prime.test. admin.jec-prime.test. 1 3600 180 1209600 180")
	askedAuthorities(t, serveDNS(t, map[string]dns.RR{"jec-prime.test.": soa}))
	if err := PrimeZone(t.Context(), "_acme-challenge.www.jec-prime.test"); err != nil {
		t.Fatal(err)
	}
	zone, err := dns01.DefaultClient().FindZoneByFqdn(t.Context(), "_acme-challenge.www.jec-prime.test.")
	if err != nil || zone != "jec-prime.test." {
		t.Errorf("zone %q, %v", zone, err)
	}
}

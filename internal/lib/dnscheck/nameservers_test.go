package dnscheck

import (
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/miekg/dns"
)

// serveRecords answers, as a resolver would, with the records of rrs that
// have the name and type asked for.
func serveRecords(t *testing.T, rrs ...string) string {
	t.Helper()
	var records []dns.RR
	for _, s := range rrs {
		rr, err := dns.NewRR(s)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, rr)
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, m *dns.Msg) {
		out := new(dns.Msg)
		out.SetReply(m)
		out.RecursionAvailable = true
		q := m.Question[0]
		for _, rr := range records {
			h := rr.Header()
			if strings.EqualFold(h.Name, q.Name) && h.Rrtype == q.Qtype {
				out.Answer = append(out.Answer, rr)
			}
		}
		if len(out.Answer) == 0 {
			out.Rcode = dns.RcodeNameError
			for _, rr := range records {
				if soa, ok := rr.(*dns.SOA); ok && dns.IsSubDomain(soa.Hdr.Name, q.Name) {
					out.Rcode = dns.RcodeSuccess
				}
			}
		}
		_ = w.WriteMsg(out)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

// useResolvers is UseResolvers for one test.
func useResolvers(t *testing.T, servers ...string) {
	t.Helper()
	UseResolvers(servers)
	t.Cleanup(func() {
		caaMu.Lock()
		caaServers = nil
		caaMu.Unlock()
		dns01.SetDefaultClient(dns01.NewClient(dns01.NewOptions()))
	})
}

func TestNameServers(t *testing.T) {
	addr := serveRecords(t,
		"example.com. 60 IN SOA kara.ns.cloudflare.com. dns.cloudflare.com. 1 7200 3600 1209600 60",
		"example.com. 60 IN NS Kara.NS.Cloudflare.com.",
		"example.com. 60 IN NS bob.ns.cloudflare.com.",
		"_acme-challenge.other.example.com. 60 IN CNAME _acme-challenge.example.org.",
		"example.org. 60 IN SOA f1g1ns1.dnspod.net. freednsadmin.dnspod.com. 1 3600 180 1209600 180",
		"example.org. 60 IN NS f1g1ns1.dnspod.net.",
	)
	useResolvers(t, addr)
	useAuthorities(t, addr)
	for _, c := range []struct {
		domain, zone string
		hosts        []string
	}{
		{"*.www.example.com", "example.com", []string{"bob.ns.cloudflare.com", "kara.ns.cloudflare.com"}},
		// Its validation records are delegated to another zone.
		{"other.example.com", "example.org", []string{"f1g1ns1.dnspod.net"}},
	} {
		zone, hosts, err := NameServers(t.Context(), c.domain)
		if err != nil || zone != c.zone || !slices.Equal(hosts, c.hosts) {
			t.Errorf("%s: %s %v, %v", c.domain, zone, hosts, err)
		}
	}
}

func TestServedBy(t *testing.T) {
	cloudflare := []string{"bob.ns.cloudflare.com", "kara.ns.cloudflare.com"}
	route53 := []string{"ns-1536.awsdns-00.co.uk", "ns-0.awsdns-00.com"}
	for _, c := range []struct {
		hosts, servers []string
		want           bool
	}{
		{cloudflare, []string{"ns.cloudflare.com"}, true},
		{cloudflare, []string{"dnspod.net", "dnsv2.com"}, false},
		{[]string{"NS1.DNSV2.COM."}, []string{"dnspod.net", "dnsv2.com"}, true},
		{[]string{"evilns.cloudflare.com.example"}, []string{"ns.cloudflare.com"}, false},
		{[]string{"notcloudflare.com"}, []string{"cloudflare.com"}, false},
		{route53, []string{"awsdns-"}, true},
		{cloudflare, []string{"awsdns-"}, false},
	} {
		if got := ServedBy(c.hosts, c.servers); got != c.want {
			t.Errorf("%v by %v: %v", c.hosts, c.servers, got)
		}
	}
}

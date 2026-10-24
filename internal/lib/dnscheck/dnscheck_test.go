package dnscheck

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

// serveDNS serves records on a local UDP port, as an authoritative server,
// and returns its address.
func serveDNS(t *testing.T, records map[string]dns.RR) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, m *dns.Msg) {
		out := new(dns.Msg)
		out.SetReply(m)
		out.Authoritative = true
		q := m.Question[0]
		if rr, ok := records[strings.ToLower(q.Name)]; ok && rr.Header().Rrtype == q.Qtype {
			out.Answer = append(out.Answer, rr)
		}
		_ = w.WriteMsg(out)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func TestVisible(t *testing.T) {
	txtRR, _ := dns.NewRR(`_dnsauth.www.example.com. 60 IN TXT "2026101900000000" "abcdef"`)
	cnameRR, _ := dns.NewRR(`_abc.example.com. 60 IN CNAME _def.sectigo.com.`)
	full := serveDNS(t, map[string]dns.RR{"_dnsauth.www.example.com.": txtRR, "_abc.example.com.": cnameRR})
	empty := serveDNS(t, map[string]dns.RR{})
	txt := record{"_dnsauth.www.example.com", "TXT", "2026101900000000abcdef"}
	cname := record{"_abc.example.com", "CNAME", "_DEF.sectigo.com"}
	for _, c := range []struct {
		r       record
		servers []string
		want    bool
	}{
		{txt, []string{full}, true},
		{cname, []string{full}, true},
		{record{txt.name, "TXT", "other"}, []string{full}, false},
		{txt, []string{full, empty}, false}, // one server lags
	} {
		got, err := visibleAt(t.Context(), c.r, c.servers)
		if err != nil || got != c.want {
			t.Errorf("%+v at %v: %v, %v", c.r, c.servers, got, err)
		}
	}
	// A server that does not answer is skipped, unless none does.
	dead := "127.0.0.1:1"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if got, err := visibleAt(ctx, txt, []string{dead, full}); err != nil || !got {
		t.Errorf("with a dead server: %v, %v", got, err)
	}
	if _, err := visibleAt(ctx, txt, []string{dead}); err == nil {
		t.Error("no server answered, yet no error")
	}
}

func TestRecordName(t *testing.T) {
	for _, c := range []struct{ name, domain, want string }{
		{"_abc.example.com.", "example.com", "_abc.example.com"},
		{"_ABC", "example.com", "_abc.example.com"},
		{"_abc", "www.example.com", "_abc.www.example.com"},
		{"_dnsauth.www", "www.example.com", "_dnsauth.www.example.com"},
		{"_dnsauth.a.b", "a.b.example.com", "_dnsauth.a.b.example.com"},
		{"_dnsauth.b", "a.b.example.com", "_dnsauth.b.a.b.example.com"},
		{"@", "www.example.com", "www.example.com"},
		{"www", "www.example.com", "www.example.com"},
		{"example.com", "www.example.com", "example.com"},
		{"_abc.example.com", "*.example.com", "_abc.example.com"},
		{"_abc.example.org", "example.com", "_abc.example.org.example.com"}, // never outside the domain
		{"_abc.co.uk", "example.co.uk", "_abc.co.uk.example.co.uk"},
		{"_abc", "co.uk", ""},
		{" ", "example.com", ""},
	} {
		if got := RecordName(c.name, c.domain); got != c.want {
			t.Errorf("RecordName(%q, %q) = %q, want %q", c.name, c.domain, got, c.want)
		}
	}
}

func TestCloudflareDCV(t *testing.T) {
	for name, want := range map[string]bool{
		"umi.li.d344984ae74e6f93.dcv.cloudflare.com":  true,
		"umi.li.d344984ae74e6f93.DCV.cloudflare.com.": true,
		"dcv.cloudflare.com":                          false,
		"acme.example.net":                            false,
		"x.dcv.cloudflare.com.example.net":            false,
	} {
		if got := CloudflareDCV(name); got != want {
			t.Errorf("CloudflareDCV(%q) = %v", name, got)
		}
	}
}

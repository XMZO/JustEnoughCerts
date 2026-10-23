package dnscheck

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

// serveZone answers as the authoritative servers of example.com do, where
// *.wild.example.com is a CNAME to cdn.example.net and
// _acme-challenge.own.example.com one to _acme-challenge.example.org.
func serveZone(t *testing.T) string {
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
		name := strings.ToLower(q.Name)
		target := ""
		switch {
		case name == "_acme-challenge.own.example.com.":
			target = "_acme-challenge.example.org."
		case strings.HasSuffix(name, ".wild.example.com."):
			target = "cdn.example.net."
		default:
			out.Rcode = dns.RcodeNameError
		}
		if target != "" && q.Qtype == dns.TypeCNAME {
			out.Answer = append(out.Answer, &dns.CNAME{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300}, Target: target})
		}
		_ = w.WriteMsg(out)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

// useAuthorities makes servers the authoritative servers of every zone,
// for one test.
func useAuthorities(t *testing.T, servers ...string) {
	t.Helper()
	saved := authorities
	authorities = func(context.Context, string) ([]string, error) { return servers, nil }
	t.Cleanup(func() { authorities = saved })
}

func TestWildcard(t *testing.T) {
	useAuthorities(t, serveZone(t))
	for _, c := range []struct {
		domain           string
		wildcard, follow bool
	}{
		{"wild.example.com", true, false},
		{"*.www.wild.example.com", true, false},
		{"own.example.com", false, true},
		// No CNAME at all: none a resolver has cached is to be followed.
		{"plain.example.com", false, false},
	} {
		if got := Wildcard(t.Context(), c.domain); got != c.wildcard {
			t.Errorf("Wildcard(%s) = %v", c.domain, got)
		}
		if got := Follow(t.Context(), c.domain); got != c.follow {
			t.Errorf("Follow(%s) = %v", c.domain, got)
		}
	}
	if got := Delegated(t.Context(), "wild.example.com"); got != "" {
		t.Errorf("Delegated through a wildcard: %q", got)
	}
}

// TestWildcardUnasked: when the servers cannot be asked, lego's way stands.
func TestWildcardUnasked(t *testing.T) {
	useAuthorities(t, "127.0.0.1:1")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if Wildcard(ctx, "wild.example.com") || !Follow(ctx, "wild.example.com") {
		t.Error("decided without an answer")
	}
}

// TestLiveWildcard asks the real authoritative servers of the domain in
// JEC_TEST_WILDCARD, which has a wildcard CNAME.
func TestLiveWildcard(t *testing.T) {
	domain := os.Getenv("JEC_TEST_WILDCARD")
	if domain == "" {
		t.Skip("JEC_TEST_WILDCARD not set")
	}
	if !Wildcard(t.Context(), domain) || Follow(t.Context(), domain) || Delegated(t.Context(), domain) != "" {
		t.Errorf("%s: the wildcard's CNAME is followed", domain)
	}
	zone, hosts, err := NameServers(t.Context(), domain)
	t.Logf("zone %s, servers %v, %v", zone, hosts, err)
}

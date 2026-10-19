package dnscheck

import (
	"net"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

// serveCAA answers as a recursive resolver with the CAA records of zones,
// by name, as zone files write them.
func serveCAA(t *testing.T, zones map[string][]string) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, m *dns.Msg) {
		out := new(dns.Msg)
		out.SetReply(m)
		out.RecursionAvailable = true
		q := m.Question[0]
		for _, s := range zones[strings.ToLower(q.Name)] {
			rr, err := dns.NewRR(s)
			if err != nil {
				t.Error(err)
			}
			out.Answer = append(out.Answer, rr)
		}
		_ = w.WriteMsg(out)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	caaMu.Lock()
	saved := caaServers
	caaServers = []string{pc.LocalAddr().String()}
	caaMu.Unlock()
	t.Cleanup(func() {
		caaMu.Lock()
		caaServers = saved
		caaMu.Unlock()
	})
}

func TestCAA(t *testing.T) {
	serveCAA(t, map[string][]string{
		"example.com.": {
			`example.com. 60 IN CAA 0 issue "letsencrypt.org"`,
			`example.com. 60 IN CAA 0 issue "pki.goog; cansignhttpexchanges=yes"`,
			`example.com. 60 IN CAA 0 issuewild ";"`,
			`example.com. 60 IN CAA 0 iodef "mailto:security@example.com"`,
		},
		"mail.example.org.": {`mail.example.org. 60 IN CAA 0 iodef "mailto:x@example.org"`},
	})
	le, google, sectigo := []string{"letsencrypt.org"}, []string{"pki.goog"}, []string{"sectigo.com"}
	// The records of the domain above decide for one without its own.
	p, err := CAA(t.Context(), "www.shop.example.com")
	if err != nil || !p.Found || !p.Allows(le, false) || !p.Allows(google, false) || p.Allows(sectigo, false) {
		t.Errorf("www.shop.example.com: %+v, %v", p, err)
	}
	// Wildcards: issuewild ";" lets no one.
	if p, err := CAA(t.Context(), "*.example.com"); err != nil || p.Allows(le, true) || !p.Wild || len(p.IssueWild) != 1 || p.IssueWild[0] != "" {
		t.Errorf("*.example.com: %+v, %v", p, err)
	}
	// No CAA up the tree, or one with no issue records: any CA.
	for _, d := range []string{"example.net", "mail.example.org"} {
		if p, err := CAA(t.Context(), d); err != nil || !p.Allows(sectigo, false) {
			t.Errorf("%s: %+v, %v", d, p, err)
		}
	}
}

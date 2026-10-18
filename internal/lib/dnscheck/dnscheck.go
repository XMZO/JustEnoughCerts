// Package dnscheck looks a DNS record up on the authoritative servers of
// its zone, as a CA that checks a validation record does, without the
// caches of recursive resolvers in the way. It needs nothing of the DNS
// providers' SDKs, so that a source may wait for its records without them.
package dnscheck

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/miekg/dns"
	"golang.org/x/net/publicsuffix"
)

// RecordName returns the full name, without a final dot, of the record a
// CA or a vendor names for validating domain, whichever way it names it:
// in full within the registrable domain of domain; "@" for domain itself;
// as a host record of the registrable domain that ends in the labels of
// domain below it, as consoles show it (_dnsauth.www for www.example.com);
// or else relative to domain. So the record never goes outside the
// registrable domain. It is empty when domain has none.
func RecordName(name, domain string) string {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	domain = strings.ToLower(strings.TrimPrefix(strings.TrimSuffix(domain, "."), "*."))
	base, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil || name == "" {
		return ""
	}
	sub := strings.TrimSuffix(strings.TrimSuffix(domain, base), ".")
	switch {
	case name == "@":
		return domain
	case name == base || strings.HasSuffix(name, "."+base):
		return name
	case sub != "" && (name == sub || strings.HasSuffix(name, "."+sub)):
		return name + "." + base
	}
	return name + "." + domain
}

// Zone returns the zone name belongs to, as its SOA says, without a final
// dot.
func Zone(ctx context.Context, name string) (string, error) {
	zone, err := dns01.DefaultClient().FindZoneByFqdn(ctx, strings.TrimSuffix(name, ".")+".")
	if err != nil {
		return "", fmt.Errorf("dnscheck: the zone of %s: %w", name, err)
	}
	return dns01.UnFqdn(zone), nil
}

// Visible reports whether the authoritative servers of the zone of name
// answer the record of typ, TXT or CNAME, with value. Each server that
// answers must have it; one that does not answer is skipped, unless none
// does.
func Visible(ctx context.Context, name, typ, value string) (bool, error) {
	zone, err := Zone(ctx, name)
	if err != nil {
		return false, err
	}
	ns, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil {
		return false, fmt.Errorf("dnscheck: the name servers of %s: %w", zone, err)
	}
	var servers []string
	for _, n := range ns {
		servers = append(servers, net.JoinHostPort(strings.TrimSuffix(n.Host, "."), "53"))
	}
	return visibleAt(ctx, record{name, typ, value}, servers)
}

type record struct{ name, typ, value string }

// visibleAt asks servers, as host:port, for r.
func visibleAt(ctx context.Context, r record, servers []string) (bool, error) {
	if len(servers) == 0 {
		return false, errors.New("dnscheck: no name server to ask")
	}
	var last error
	answered := 0
	for _, s := range servers {
		ok, err := answers(ctx, r, s)
		if err != nil {
			last = err
			continue
		}
		if !ok {
			return false, nil
		}
		answered++
	}
	if answered == 0 {
		return false, last
	}
	return true, nil
}

// answers reports whether the server at addr answers r.
func answers(ctx context.Context, r record, addr string) (bool, error) {
	qtype := dns.TypeTXT
	if strings.EqualFold(r.typ, "CNAME") {
		qtype = dns.TypeCNAME
	}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(r.name), qtype)
	m.RecursionDesired = false
	c := &dns.Client{Timeout: 5 * time.Second}
	in, _, err := c.ExchangeContext(ctx, m, addr)
	if err == nil && in.Truncated {
		c.Net = "tcp"
		in, _, err = c.ExchangeContext(ctx, m, addr)
	}
	if err != nil {
		return false, fmt.Errorf("dnscheck: ask %s: %w", addr, err)
	}
	for _, rr := range in.Answer {
		switch v := rr.(type) {
		case *dns.TXT:
			if strings.Join(v.Txt, "") == r.value {
				return true, nil
			}
		case *dns.CNAME:
			if strings.EqualFold(strings.TrimSuffix(v.Target, "."), strings.TrimSuffix(r.value, ".")) {
				return true, nil
			}
		}
	}
	return false, nil
}

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
	"net/netip"
	"slices"
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

// MainlandChina and Abroad stand for visitors from mainland China and
// from elsewhere: subnets they come from, as the authoritative servers of
// a zone that answers by region see them (EDNS Client Subnet, RFC 7871).
var (
	MainlandChina = netip.MustParsePrefix("114.114.114.0/24")
	Abroad        = netip.MustParsePrefix("8.8.8.0/24")
)

// Target returns where the authoritative servers of the zone of name send
// visitors from subnet: the name that the CNAMEs of name lead to beyond
// what they answer, without a final dot, or else the addresses of name,
// sorted and joined by commas, so that two targets compare as strings.
// Servers that do not answer by region answer as for anyone.
func Target(ctx context.Context, name string, subnet netip.Prefix) (string, error) {
	zone, err := Zone(ctx, name)
	if err != nil {
		return "", err
	}
	ns, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil {
		return "", fmt.Errorf("dnscheck: the name servers of %s: %w", zone, err)
	}
	last := errors.New("dnscheck: no name server to ask")
	for _, n := range ns {
		target, err := targetAt(ctx, name, subnet, net.JoinHostPort(strings.TrimSuffix(n.Host, "."), "53"))
		if err == nil {
			return target, nil
		}
		last = err
	}
	return "", last
}

// targetAt asks the server at addr where visitors from subnet go for name.
func targetAt(ctx context.Context, name string, subnet netip.Prefix, addr string) (string, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	m.RecursionDesired = false
	family := uint16(1)
	if subnet.Addr().Is6() {
		family = 2
	}
	opt := &dns.OPT{Hdr: dns.RR_Header{Name: ".", Rrtype: dns.TypeOPT}}
	opt.SetUDPSize(1232)
	opt.Option = append(opt.Option, &dns.EDNS0_SUBNET{
		Code: dns.EDNS0SUBNET, Family: family, Address: net.IP(subnet.Addr().AsSlice()),
		SourceNetmask: uint8(subnet.Bits()), //nolint:gosec // the length of a prefix is at most 128
	})
	m.Extra = append(m.Extra, opt)
	c := &dns.Client{Timeout: 5 * time.Second}
	in, _, err := c.ExchangeContext(ctx, m, addr)
	if err == nil && in.Truncated {
		c.Net = "tcp"
		in, _, err = c.ExchangeContext(ctx, m, addr)
	}
	if err != nil {
		return "", fmt.Errorf("dnscheck: ask %s: %w", addr, err)
	}
	at := dns.Fqdn(name)
	for range 10 { // the CNAMEs the server answers with
		next := ""
		var addrs []string
		for _, rr := range in.Answer {
			if !strings.EqualFold(rr.Header().Name, at) {
				continue
			}
			switch v := rr.(type) {
			case *dns.A:
				addrs = append(addrs, v.A.String())
			case *dns.CNAME:
				next = v.Target
			}
		}
		if len(addrs) > 0 {
			slices.Sort(addrs)
			return strings.Join(addrs, ","), nil
		}
		if next == "" {
			break
		}
		at = next
	}
	if !strings.EqualFold(at, dns.Fqdn(name)) {
		return strings.ToLower(strings.TrimSuffix(at, ".")), nil
	}
	return "", fmt.Errorf("dnscheck: %s has no address at %s", name, addr)
}

package dnscheck

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// CAAPolicy is what the CAA records of a domain say (RFC 8659): which CAs
// may issue certificates for it, and for wildcards of it.
type CAAPolicy struct {
	// Found tells that a domain on the way up has CAA records; without
	// any, every CA may issue.
	Found bool
	// Issue and IssueWild are the domains of the CAs that the issue and
	// issuewild records name; "" for a record that names none. Wild tells
	// that there are issuewild records, which then decide for wildcards.
	Issue, IssueWild []string
	Wild             bool
}

// Allows reports whether the policy lets a CA known by one of ids issue a
// certificate for a name, a wildcard or not.
func (p CAAPolicy) Allows(ids []string, wildcard bool) bool {
	if !p.Found {
		return true
	}
	list := p.Issue
	if wildcard && p.Wild {
		list = p.IssueWild
	}
	if list == nil {
		return true // only other tags, such as iodef
	}
	for _, v := range list {
		for _, id := range ids {
			if v != "" && strings.EqualFold(v, id) {
				return true
			}
		}
	}
	return false
}

// CAA returns the CAA policy of domain: that of the first name, from
// domain up to its top level, that has CAA records, as the recursive
// resolvers answer, following CNAMEs.
func CAA(ctx context.Context, domain string) (CAAPolicy, error) {
	var p CAAPolicy
	name := strings.ToLower(strings.TrimPrefix(strings.TrimSuffix(domain, "."), "*."))
	for name != "" {
		records, err := lookupCAA(ctx, name)
		if err != nil {
			return p, err
		}
		if len(records) > 0 {
			p.Found = true
			for _, r := range records {
				value, _, _ := strings.Cut(r.Value, ";")
				value = strings.ToLower(strings.TrimSpace(value))
				switch strings.ToLower(r.Tag) {
				case "issue":
					p.Issue = append(p.Issue, value)
				case "issuewild":
					p.Wild = true
					p.IssueWild = append(p.IssueWild, value)
				}
			}
			return p, nil
		}
		_, name, _ = strings.Cut(name, ".")
	}
	return p, nil
}

// caaServers are the recursive resolvers CAA asks: those UseResolvers
// set, or else the system's; tests set them.
var (
	caaMu      sync.Mutex
	caaServers []string
)

func recursiveServers() ([]string, error) {
	caaMu.Lock()
	defer caaMu.Unlock()
	if len(caaServers) > 0 {
		return caaServers, nil
	}
	cfg, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil || len(cfg.Servers) == 0 {
		return nil, errors.New("dnscheck: no recursive resolver to ask")
	}
	var out []string
	for _, s := range cfg.Servers {
		out = append(out, net.JoinHostPort(strings.Trim(s, "[]"), cfg.Port))
	}
	return out, nil
}

// lookupCAA asks the recursive resolvers for the CAA records of name.
func lookupCAA(ctx context.Context, name string) ([]*dns.CAA, error) {
	servers, err := recursiveServers()
	if err != nil {
		return nil, err
	}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeCAA)
	m.RecursionDesired = true
	c := &dns.Client{Timeout: 5 * time.Second}
	var last error
	for _, s := range servers {
		in, _, err := c.ExchangeContext(ctx, m, s)
		if err == nil && in.Truncated {
			tcp := &dns.Client{Net: "tcp", Timeout: 5 * time.Second}
			in, _, err = tcp.ExchangeContext(ctx, m, s)
		}
		switch {
		case err != nil:
			last = fmt.Errorf("dnscheck: ask %s: %w", s, err)
			continue
		case in.Rcode != dns.RcodeSuccess && in.Rcode != dns.RcodeNameError:
			last = fmt.Errorf("dnscheck: %s answers %s for the CAA of %s", s, dns.RcodeToString[in.Rcode], name)
			continue
		}
		var out []*dns.CAA
		for _, rr := range in.Answer {
			if caa, ok := rr.(*dns.CAA); ok {
				out = append(out, caa)
			}
		}
		return out, nil
	}
	return nil, last
}

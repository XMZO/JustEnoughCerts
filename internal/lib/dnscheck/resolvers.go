package dnscheck

import (
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/go-acme/lego/v5/challenge/dns01"
)

// ResolversEnv is the variable that carries the resolvers to the child
// processes of lego's DNS providers, which find zones too.
const ResolversEnv = "JEC_DNS_RESOLVERS"

// ParseResolvers reads a list of recursive DNS resolvers (PLAN.md 11), such
// as 223.5.5.5, 119.29.29.29:53 or [2400:3200::1]:53, separated by commas
// or spaces, as host:port. It fails for an entry that is no address.
func ParseResolvers(list string) ([]string, error) {
	var out []string
	for _, entry := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		host, port := entry, "53"
		if h, p, err := net.SplitHostPort(entry); err == nil {
			host, port = h, p
		} else if strings.Count(entry, ":") > 1 {
			host = strings.Trim(entry, "[]") // an IPv6 address without a port
		}
		if !resolverHost(host) || !resolverPort(port) {
			return nil, fmt.Errorf("dnscheck: %q is no DNS resolver: give an address, with a port or not", entry)
		}
		out = append(out, net.JoinHostPort(host, port))
	}
	return out, nil
}

func resolverHost(h string) bool {
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || strings.Trim(label, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" {
			return false
		}
	}
	return true
}

func resolverPort(p string) bool {
	n, err := netip.ParseAddrPort("127.0.0.1:" + p)
	return err == nil && n.Port() != 0
}

// UseResolvers makes servers the recursive resolvers that find the zones
// of names, for lego and this package alike, in this process; none keeps
// those of the system, from /etc/resolv.conf.
func UseResolvers(servers []string) {
	if len(servers) == 0 {
		return
	}
	opts := dns01.NewOptions()
	opts.RecursiveNameservers = servers
	dns01.SetDefaultClient(dns01.NewClient(opts))
}

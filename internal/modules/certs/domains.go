package certs

import (
	"net/netip"
	"strings"

	"golang.org/x/net/idna"
)

// domainProfile checks names as they are looked up, and turns an
// internationalized name into its ASCII form.
var domainProfile = idna.New(idna.MapForLookup(), idna.BidiRule(), idna.VerifyDNSLength(true))

// normalizeDomain returns the ASCII form of a domain name, in lowercase and
// without a final dot, or false for a malformed one. A wildcard takes the
// form *.example.com. An IP address, which some CAs certify too, comes in
// its usual form: 192.0.2.1, 2001:db8::1.
func normalizeDomain(name string) (string, bool) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if ip, err := netip.ParseAddr(strings.Trim(name, "[]")); err == nil {
		if ip.Zone() != "" {
			return "", false
		}
		return ip.Unmap().String(), true
	}
	base, wildcard := strings.CutPrefix(name, "*.")
	if _, err := netip.ParseAddr(base); base == "" || strings.Contains(base, "*") || err == nil {
		return "", false // an address has no names under it
	}
	ascii, err := domainProfile.ToASCII(base)
	if err != nil || ascii == "" || len(ascii) > 253 {
		return "", false
	}
	ascii = strings.ToLower(ascii)
	if wildcard {
		ascii = "*." + ascii
	}
	return ascii, true
}

// isWildcard reports whether a normalized domain is a wildcard.
func isWildcard(domain string) bool { return strings.HasPrefix(domain, "*.") }

// isIP reports whether a normalized domain is an IP address, which only
// files on the web server, never DNS, can prove.
func isIP(domain string) bool {
	_, err := netip.ParseAddr(domain)
	return err == nil
}

// normalizeDomains normalizes names and drops repeats, keeping the order.
// bad lists the indexes of the malformed names.
func normalizeDomains(names []string) (out []string, bad []int) {
	seen := map[string]bool{}
	for i, name := range names {
		n, ok := normalizeDomain(name)
		switch {
		case !ok:
			bad = append(bad, i)
		case !seen[n]:
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, bad
}

// uncovered returns the domains that the certificate names do not cover,
// in order. A name covers itself only: a wildcard asked for must be in the
// certificate as a wildcard.
func uncovered(domains, names []string) []string {
	have := map[string]bool{}
	for _, n := range names {
		have[strings.ToLower(n)] = true
	}
	var missing []string
	for _, d := range domains {
		if !have[d] {
			missing = append(missing, d)
		}
	}
	return missing
}

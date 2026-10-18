// Package clientip finds the client of a request, also behind reverse
// proxies, whose X-Forwarded-For it trusts when told to (PLAN.md 11).
package clientip

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// Resolver finds the client of requests: their peer, or, when the peer is
// a trusted proxy, the address that proxy says it forwarded for.
type Resolver struct {
	trusted []netip.Prefix
}

// New returns a Resolver that trusts the proxies of spec: addresses and
// CIDR ranges, separated by commas or spaces. An empty spec trusts none,
// so that X-Forwarded-For, which any client can send, is never read.
func New(spec string) (*Resolver, error) {
	r := &Resolver{}
	for _, field := range strings.FieldsFunc(spec, func(c rune) bool { return c == ',' || c == ' ' }) {
		if p, err := netip.ParsePrefix(field); err == nil {
			r.trusted = append(r.trusted, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(field)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: want an address or a CIDR range", field)
		}
		r.trusted = append(r.trusted, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return r, nil
}

func (r *Resolver) trusts(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Client returns the client of a request from remoteAddr, its peer as
// net/http has it, and its X-Forwarded-For headers. Behind trusted proxies
// it is the last address of X-Forwarded-For that is not one of them: each
// proxy appends the address it heard from, so what comes before the first
// untrusted one may be made up. Without trusted proxies, or when the
// header is missing or malformed, it is the peer.
func (r *Resolver) Client(remoteAddr string, forwardedFor []string) string {
	peer := remoteAddr
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		peer = host
	}
	a, err := netip.ParseAddr(peer)
	if err != nil || !r.trusts(a) {
		return peer
	}
	var hops []string
	for _, h := range forwardedFor {
		hops = append(hops, strings.Split(h, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return client
		}
		client = hop.Unmap().String()
		if !r.trusts(hop) {
			return client
		}
	}
	return client
}

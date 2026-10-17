// Package httpx makes the HTTP clients that talk to services people set
// up, such as a chat bot or a webhook: with timeouts, a proxy of their
// own instead of the environment's, and, where the address comes from a
// person, a guard that keeps requests off private networks (PLAN.md 10).
package httpx

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"syscall"
	"time"
)

// Options of a Client.
type Options struct {
	// Timeout bounds a whole request; zero means 30 seconds.
	Timeout time.Duration
	// Proxy is the URL of an HTTP, HTTPS or SOCKS5 proxy; empty for none.
	// Proxies set in the environment are not used.
	Proxy string
	// PublicOnly refuses to connect to an address that is not on the public
	// internet (see Public), wherever a redirect leads. It cannot be
	// combined with a proxy, whose own address is the one dialed.
	PublicOnly bool
}

// Errors of Client and of the requests it makes.
var (
	ErrProxy     = errors.New("httpx: the proxy must be an http, https or socks5 URL")
	ErrNotPublic = errors.New("httpx: the address is not on the public internet")
)

// Client returns a client with o.
func Client(o Options) (*http.Client, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if o.PublicOnly {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return err
			}
			if !Public(ap.Addr()) {
				return fmt.Errorf("%w: %s", ErrNotPublic, ap.Addr())
			}
			return nil
		}
	}
	t := &http.Transport{
		DialContext:         dialer.DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
	}
	if o.Proxy != "" {
		if o.PublicOnly {
			return nil, errors.New("httpx: PublicOnly does not go with a proxy")
		}
		u, err := url.Parse(o.Proxy)
		if err != nil || u.Host == "" || !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, u.Scheme) {
			return nil, ErrProxy
		}
		t.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: t, Timeout: cmp.Or(o.Timeout, 30*time.Second)}, nil
}

// notPublic are the ranges Public refuses beyond what netip tells apart:
// shared address space (carrier-grade NAT), IETF protocol assignments,
// benchmarking, and IPv6 prefixes that embed IPv4 addresses.
var notPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
}

// Public reports whether a is an address on the public internet: not
// loopback, private, link-local, multicast, unspecified, shared or
// otherwise reserved.
func Public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range notPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// Cause returns err without the URL that an *url.Error adds, which may
// hold a secret such as a bot token.
func Cause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

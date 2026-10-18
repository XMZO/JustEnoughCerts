// Package httpx makes the HTTP clients that talk to services people set
// up, such as a chat bot or a webhook: with timeouts, a proxy of their
// own instead of the environment's, and, where the address comes from a
// person, a guard that keeps requests off private networks (PLAN.md 10).
package httpx

import (
	"cmp"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
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
	// SkipVerify accepts any certificate over HTTPS, as the HTTP validation
	// of a CA does when a redirect leads there: the website may still have
	// the expired certificate being replaced. It is for checking the public
	// files of validations, never for sending anything.
	SkipVerify bool
	// PrivateOnly refuses to connect to an address on the public internet:
	// for plain HTTP that carries secrets, which must not cross it.
	PrivateOnly bool
	// Pin trusts the server whose certificate has this SHA-256 fingerprint,
	// in hex with colons or without, in place of the usual checks: for a
	// server people set up with a certificate of its own making, such as a
	// panel, as SSH trusts a host key. Fingerprint tells it from the error
	// of a connection that did not pin it.
	Pin string
}

// Errors of Client and of the requests it makes.
var (
	ErrProxy     = errors.New("httpx: the proxy must be an http, https or socks5 URL")
	ErrNotPublic = errors.New("httpx: the address is not on the public internet")
	ErrPublic    = errors.New("httpx: the address is on the public internet")
	ErrPin       = errors.New("httpx: the certificate does not have the fingerprint trusted")
)

// Client returns a client with o.
func Client(o Options) (*http.Client, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if o.PublicOnly && o.PrivateOnly {
		return nil, errors.New("httpx: PublicOnly and PrivateOnly leave no address")
	}
	if o.PublicOnly || o.PrivateOnly {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return err
			}
			switch public := Public(ap.Addr()); {
			case o.PublicOnly && !public:
				return fmt.Errorf("%w: %s", ErrNotPublic, ap.Addr())
			case o.PrivateOnly && public:
				return fmt.Errorf("%w: %s", ErrPublic, ap.Addr())
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
	switch {
	case o.SkipVerify:
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // fetches public files as a CA does; see Options.SkipVerify
	case o.Pin != "":
		pin, err := hex.DecodeString(strings.NewReplacer(":", "", " ", "").Replace(o.Pin))
		if err != nil || len(pin) != sha256.Size {
			return nil, fmt.Errorf("httpx: %q is no SHA-256 fingerprint", o.Pin)
		}
		t.TLSClientConfig = &tls.Config{
			// The pin is the check: VerifyConnection compares it.
			InsecureSkipVerify: true, //nolint:gosec // the certificate is checked against the pinned fingerprint
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return ErrPin
				}
				sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
				if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
					return fmt.Errorf("%w: it has %s", ErrPin, format(sum[:]))
				}
				return nil
			},
		}
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

// Fingerprint returns the SHA-256 fingerprint of the certificate a server
// presented that could not be verified, for people to check and pin, as
// Options.Pin takes it.
func Fingerprint(err error) (string, bool) {
	var v *tls.CertificateVerificationError
	if !errors.As(err, &v) || len(v.UnverifiedCertificates) == 0 {
		return "", false
	}
	sum := sha256.Sum256(v.UnverifiedCertificates[0].Raw)
	return format(sum[:]), true
}

// format writes a fingerprint as browsers show it: hex pairs in capitals,
// between colons.
func format(sum []byte) string {
	pairs := make([]string, len(sum))
	for i, b := range sum {
		pairs[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(pairs, ":")
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

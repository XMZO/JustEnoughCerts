package watch

import (
	"context"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"
)

// WHOIS (RFC 3912), for the TLDs whose registries have no RDAP, as .cn
// and .io: IANA names the server of each TLD, which answers free text, of
// which the end and the registrar are read where they are written in one
// of the usual ways.
const (
	ianaWhois = "whois.iana.org"
	maxWhois  = 64 << 10
)

// whoisServers fills what IANA leaves out.
var whoisServers = map[string]string{"uk": "whois.nic.uk"}

// The lines that tell the end, the most telling first, and the registrar.
var (
	whoisEnds = []*regexp.Regexp{
		regexp.MustCompile(`(?im)^\s*registry expiry date\s*:\s*(\S.*)$`),
		regexp.MustCompile(`(?im)^\s*expiration time\s*:\s*(\S.*)$`),
		regexp.MustCompile(`(?im)^\s*registrar registration expiration date\s*:\s*(\S.*)$`),
		regexp.MustCompile(`(?im)^\s*\[?(?:expiration date|expiry date|expires on|expire date|expires|paid-till|valid until|renewal date)\]?\s*:?\s+(\S.*)$`),
	}
	whoisRegistrar = regexp.MustCompile(`(?im)^\s*(?:sponsoring )?registrar\s*:\s*(\S.*)$`)
)

// whoisQuery asks server about query over TCP port 43.
func whoisQuery(ctx context.Context, server, query string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(server, "43"))
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(conn, query+"\r\n"); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(conn, maxWhois))
	if err != nil && len(data) == 0 {
		return "", err
	}
	return string(data), nil
}

// whoisServer returns the WHOIS server of the TLD of domain, as IANA
// names it, or errNoRDAP without one.
func (r *rdap) whoisServer(ctx context.Context, domain string) (string, error) {
	tld := domain[strings.LastIndexByte(domain, '.')+1:]
	r.mu.Lock()
	server, ok := r.whoisOf[tld]
	r.mu.Unlock()
	if ok {
		if server == "" {
			return "", errNoRDAP
		}
		return server, nil
	}
	server = whoisServers[tld]
	if server == "" {
		answer, err := r.whois(ctx, ianaWhois, tld)
		if err != nil {
			return "", fmt.Errorf("IANA's WHOIS: %w", err)
		}
		// Blanks only: IANA leaves the line empty for a TLD without
		// WHOIS (.al), which must not read as the line after it.
		if m := regexp.MustCompile(`(?im)^whois:[ \t]*(\S+)`).FindStringSubmatch(answer); m != nil {
			server = m[1]
		}
	}
	r.mu.Lock()
	if r.whoisOf == nil {
		r.whoisOf = map[string]string{}
	}
	r.whoisOf[tld] = server
	r.mu.Unlock()
	if server == "" {
		return "", errNoRDAP
	}
	return server, nil
}

// whoisLookup asks the WHOIS server of the TLD of domain when its
// registration ends.
func (r *rdap) whoisLookup(ctx context.Context, domain string) (registration, error) {
	server, err := r.whoisServer(ctx, domain)
	if err != nil {
		return registration{}, err
	}
	answer, err := r.whois(ctx, server, domain)
	if err != nil {
		return registration{}, fmt.Errorf("WHOIS %s: %w", server, err)
	}
	return parseWhois(answer)
}

// parseWhois reads the end and the registrar out of a WHOIS answer.
func parseWhois(answer string) (registration, error) {
	var reg registration
	for _, re := range whoisEnds {
		if m := re.FindStringSubmatch(answer); m != nil {
			if t, ok := parseWhoisDate(m[1]); ok {
				reg.expires = t
				break
			}
		}
	}
	if reg.expires.IsZero() {
		return registration{}, errNoEnd
	}
	if m := whoisRegistrar.FindStringSubmatch(answer); m != nil {
		reg.registrar = strings.TrimSpace(m[1])
	}
	return reg, nil
}

// parseWhoisDate reads a date as registries write them: 2027-09-30T01:00:00Z,
// 2029-03-17 12:48:36, 13-Dec-2034, 2026/09/30 and the like.
func parseWhoisDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	candidates := []string{s}
	if f := strings.Fields(s); len(f) > 1 {
		candidates = append(candidates, f[0]+" "+f[1], f[0])
	}
	layouts := []string{
		time.RFC3339Nano, "2006-01-02T15:04:05Z", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
		"02-Jan-2006", "2006/01/02", "2006.01.02", "02.01.2006", "2-Jan-2006", "January 2 2006",
	}
	for _, c := range candidates {
		for _, layout := range layouts {
			if t, err := time.Parse(layout, c); err == nil {
				return t.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

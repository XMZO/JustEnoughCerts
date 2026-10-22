package lookup

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// WHOIS (RFC 3912) over TCP port 43, with the server of each suffix and the
// form of query it wants, from Whoice: data/whois-iana.json is IANA's list,
// data/whois-extra.json what it leaves out or gets wrong (DENIC answers
// only "-T dn <name>", JPRS in English to "<name>/e").
const (
	maxWhois     = 2 << 20
	whoisTimeout = 15 * time.Second
	// whoisFollow is how many referrals to another server are followed.
	whoisFollow = 1
)

var (
	//go:embed data/whois-iana.json
	whoisIANA []byte
	//go:embed data/whois-extra.json
	whoisExtra []byte
)

type whoisServer struct {
	host     string
	template string // the query, with {query} for the name
}

type whoisServers map[string]whoisServer

// whoisForms are the forms of query of servers the lists name plainly:
// Verisign matches name servers too unless asked for a domain ("="), JPRS
// answers in Japanese unless asked in English ("/e").
var whoisForms = whoisServers{
	"com": {host: "whois.verisign-grs.com", template: "={query}"},
	"net": {host: "whois.verisign-grs.com", template: "={query}"},
	"jp":  {host: "whois.jprs.jp", template: "{query}/e"},
}

func loadWhoisServers() whoisServers {
	servers := maps.Clone(whoisForms)
	// IANA's first, then the extra list over it.
	for _, data := range [][]byte{whoisIANA, whoisExtra} {
		parsed, err := parseWhoisServers(data)
		if err != nil {
			panic("whoice: the WHOIS server lists: " + err.Error())
		}
		for suffix, s := range parsed {
			// A plain entry keeps the form of query an earlier one gave.
			if old, ok := servers[suffix]; ok && old.template != "{query}" && s.template == "{query}" {
				s.template = old.template
			}
			servers[suffix] = s
		}
	}
	return servers
}

// parseWhoisServers reads a list of servers by suffix: a host, or an object
// with the host and the query.
func parseWhoisServers(data []byte) (whoisServers, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	servers := whoisServers{}
	for suffix, value := range raw {
		suffix = strings.Trim(strings.ToLower(strings.TrimSpace(suffix)), ".")
		var host string
		if err := json.Unmarshal(value, &host); err == nil {
			if host = strings.TrimSpace(host); host != "" && suffix != "" {
				servers[suffix] = whoisServer{host: host, template: "{query}"}
			}
			continue
		}
		var object struct {
			Host          string `json:"host"`
			Query         string `json:"query"`
			QueryTemplate string `json:"queryTemplate"`
		}
		if err := json.Unmarshal(value, &object); err != nil {
			return nil, fmt.Errorf("%s: %w", suffix, err)
		}
		if host = strings.TrimSpace(object.Host); host == "" || suffix == "" {
			continue
		}
		template := strings.TrimSpace(object.QueryTemplate)
		if template == "" {
			template = strings.TrimSpace(object.Query)
		}
		if template == "" {
			template = "{query}"
		}
		servers[suffix] = whoisServer{host: host, template: template}
	}
	return servers, nil
}

// resolve returns the server for q, with the query to send it: the one of
// the longest suffix of the name that the lists name. Under a private
// suffix (eu.org, github.io) only one for the suffix itself or below will
// do: the TLD's registry holds no such names.
func (s whoisServers) resolve(q model.NormalizedQuery) (whoisServer, string, bool) {
	labels := strings.Split(q.Query, ".")
	for i := range labels {
		suffix := strings.Join(labels[i:], ".")
		if q.PrivateSuffix && len(suffix) < len(q.Suffix) {
			break
		}
		if server, ok := s[suffix]; ok && server.host != "" {
			return server, applyTemplate(server.template, q.Query), true
		}
	}
	return whoisServer{}, "", false
}

func applyTemplate(template, query string) string {
	if template == "" {
		return query
	}
	template = strings.ReplaceAll(template, "%s", "{query}")
	template = strings.TrimRight(template, "\r\n")
	return strings.ReplaceAll(template, "{query}", query)
}

var (
	// referral is a line that names another server to ask: IANA's, or a
	// thin registry's. Its blanks stay on the line, so that an empty one
	// ("whois:" with no server) is not read as the line after it.
	referral = regexp.MustCompile(`(?im)^[ \t]*(?:whois|refer):[ \t]*([a-z0-9.-]+)[ \t]*$`)
	// refusal is a registry's notice that it does not answer: a client
	// blocked, a rate limit, a form of query it rejects.
	refusal = regexp.MustCompile(`(?im)^.*(?:requests of this client are not permitted|solo acepta consultas|(?:query|request|rate|connection) ?limit (?:exceeded|reached)|maximum (?:number of )?(?:queries|requests|query rate)|too many (?:queries|requests|connections)|access (?:has been )?denied|you have exceeded|temporarily (?:blocked|denied|unavailable)|try again later).*$`)
	// recordField is a line only a record has.
	recordField = regexp.MustCompile(`(?im)^\s*(?:domain name|domain|domainname)\s*[.:]`)
)

// whois asks the server of q's suffix about it, and a server it refers to;
// errSkip when no list names one, as for a registry without WHOIS (.al).
func (l *Lookup) whois(ctx context.Context, q model.NormalizedQuery) (*model.RawResponse, error) {
	server, query, ok := l.whoisServers.resolve(q)
	if !ok {
		return nil, errSkip
	}
	start := time.Now()
	body, err := l.whoisQuery(ctx, server.host, query)
	if err != nil {
		return nil, fmt.Errorf("WHOIS %s: %w", server.host, err)
	}
	used := []string{server.host}
	for range whoisFollow {
		m := referral.FindStringSubmatch(body)
		if m == nil || strings.EqualFold(m[1], used[len(used)-1]) || strings.EqualFold(m[1], used[0]) {
			break
		}
		more, err := l.whoisQuery(ctx, m[1], query)
		if err != nil {
			break
		}
		body += "\n\n--- " + m[1] + " ---\n" + more
		used = append(used, m[1])
	}
	if notice := refused(body); notice != "" {
		return nil, fmt.Errorf("WHOIS %s refused the query: %s", server.host, notice)
	}
	return &model.RawResponse{
		Source:    model.SourceWHOIS,
		Server:    strings.Join(used, ", "),
		Query:     q.Query,
		Body:      body,
		ElapsedMs: time.Since(start).Milliseconds(),
	}, nil
}

// whoisQuery sends query to server over TCP port 43 and reads the answer,
// in UTF-8 or, failing that, Windows-1252 or Latin-1.
func whoisQuery(ctx context.Context, server, query string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, whoisTimeout)
	defer cancel()
	if !strings.Contains(server, ":") {
		server = net.JoinHostPort(server, "43")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", server)
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
	return decode(data), nil
}

func decode(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	for _, c := range []*charmap.Charmap{charmap.Windows1252, charmap.ISO8859_1} {
		if decoded, err := c.NewDecoder().Bytes(data); err == nil && utf8.Valid(decoded) {
			return string(decoded)
		}
	}
	return strings.ToValidUTF8(string(data), "�")
}

// refused returns a registry's short notice that it does not answer, or ""
// for a record. Only short lines count: terms of use (.mu, Identity
// Digital) mention "too many queries" in a long line of a record.
func refused(body string) string {
	if recordField.MatchString(body) {
		return ""
	}
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len(line) > 300 || !refusal.MatchString(line) {
			continue
		}
		if len(line) > 160 {
			line = line[:160] + "…"
		}
		return line
	}
	return ""
}

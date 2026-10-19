package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

// RDAP (RFC 9082, 9083) tells the registration of a domain as JSON over
// HTTPS; the registries of the generic TLDs and of many country codes
// answer it. IANA's bootstrap file (RFC 9224) names the server of each TLD.
const (
	bootstrapURL = "https://data.iana.org/rdap/dns.json"
	bootstrapTTL = 24 * time.Hour
	maxAnswer    = 1 << 20 // bytes of an answer read
)

// Lookups that tell no end; the domain is looked up again much later.
var (
	errNoRDAP = errors.New("the registry of this TLD has no RDAP service")
	errNoEnd  = errors.New("the registry tells no expiration date")
)

// registration is what a registry tells of a domain.
type registration struct {
	expires   time.Time
	registrar string
}

// rdap looks domains up, with the servers of IANA's bootstrap file, which it
// fetches again once a day.
type rdap struct {
	client    *http.Client
	bootstrap string // the URL of the bootstrap file
	now       func() time.Time

	mu      sync.Mutex
	servers map[string]string // TLD to the base URL of its server
	fetched time.Time
}

// server returns the base URL of the RDAP server for domain, ending in /,
// or errNoRDAP.
func (r *rdap) server(ctx context.Context, domain string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.servers == nil || r.now().Sub(r.fetched) > bootstrapTTL {
		servers, err := r.fetchBootstrap(ctx)
		if err != nil && r.servers == nil {
			return "", err
		}
		if err == nil {
			r.servers, r.fetched = servers, r.now()
		}
	}
	// The longest suffix the file names, as it may name more than TLDs.
	for name := domain; ; {
		if base, ok := r.servers[name]; ok {
			return base, nil
		}
		i := strings.IndexByte(name, '.')
		if i < 0 {
			return "", errNoRDAP
		}
		name = name[i+1:]
	}
}

func (r *rdap) fetchBootstrap(ctx context.Context) (map[string]string, error) {
	var file struct {
		Services [][][]string `json:"services"`
	}
	if err := r.get(ctx, r.bootstrap, &file); err != nil {
		return nil, fmt.Errorf("the RDAP bootstrap file: %w", err)
	}
	servers := map[string]string{}
	for _, s := range file.Services {
		if len(s) < 2 {
			continue
		}
		base := ""
		for _, u := range s[1] {
			if strings.HasPrefix(u, "https://") || base == "" && strings.HasPrefix(u, "http://") {
				base = u
			}
		}
		if base == "" {
			continue
		}
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		for _, tld := range s[0] {
			servers[strings.ToLower(tld)] = base
		}
	}
	if len(servers) == 0 {
		return nil, errors.New("the RDAP bootstrap file names no servers")
	}
	return servers, nil
}

type rdapEntity struct {
	Roles    []string          `json:"roles"`
	VCard    []json.RawMessage `json:"vcardArray"`
	Entities []rdapEntity      `json:"entities"`
}

type rdapDomain struct {
	Events []struct {
		Action string `json:"eventAction"`
		Date   string `json:"eventDate"`
	} `json:"events"`
	Entities []rdapEntity `json:"entities"`
}

// lookup asks the registry of domain when its registration ends, and with
// which registrar.
func (r *rdap) lookup(ctx context.Context, domain string) (registration, error) {
	base, err := r.server(ctx, domain)
	if err != nil {
		return registration{}, err
	}
	var d rdapDomain
	if err := r.get(ctx, base+"domain/"+domain, &d); err != nil {
		return registration{}, err
	}
	var reg registration
	for _, e := range d.Events {
		if e.Action != "expiration" {
			continue
		}
		if t, ok := parseDate(e.Date); ok {
			reg.expires = t
		}
	}
	if reg.expires.IsZero() {
		return registration{}, errNoEnd
	}
	reg.registrar = registrar(d.Entities)
	return reg, nil
}

// get fetches url as RDAP JSON into out.
func (r *rdap) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/rdap+json, application/json")
	res, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered HTTP %d", req.URL.Host, res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxAnswer)).Decode(out); err != nil {
		return fmt.Errorf("%s answered no RDAP JSON: %w", req.URL.Host, err)
	}
	return nil
}

func parseDate(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// registrar is the name the entity in the registrar's role goes by, from
// its vCard (RFC 7095): ["vcard", [["fn", {}, "text", "Name"], …]].
func registrar(entities []rdapEntity) string {
	for _, e := range entities {
		if !contains(e.Roles, "registrar") || len(e.VCard) < 2 {
			continue
		}
		var props [][]any
		if json.Unmarshal(e.VCard[1], &props) != nil {
			continue
		}
		for _, p := range props {
			if len(p) >= 4 && p[0] == "fn" {
				if name, ok := p[3].(string); ok && name != "" {
					return name
				}
			}
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// reserved are the domains kept for examples (RFC 2606), which no one
// registers.
var reserved = map[string]bool{"example.com": true, "example.net": true, "example.org": true}

// registered returns the domain registered with a registry that name is
// under: shop.co.uk for www.shop.co.uk or *.shop.co.uk, and github.io for
// user.github.io, as suffixes run by others than registries do not count.
// It returns "" for an address, a public suffix itself, a name under no
// TLD of ICANN's, as of a private network, and a domain for examples.
func registered(name string) string {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "*.")), ".")
	if name == "" || net.ParseIP(name) != nil {
		return ""
	}
	suffix, icann := publicsuffix.PublicSuffix(name)
	for !icann {
		i := strings.IndexByte(suffix, '.')
		if i < 0 {
			return ""
		}
		suffix, icann = publicsuffix.PublicSuffix(suffix[i+1:])
	}
	if len(name) <= len(suffix) {
		return ""
	}
	rest := name[:len(name)-len(suffix)-1]
	if d := rest[strings.LastIndexByte(rest, '.')+1:] + "." + suffix; !reserved[d] {
		return d
	}
	return ""
}

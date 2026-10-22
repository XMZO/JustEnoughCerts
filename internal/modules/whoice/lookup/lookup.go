// Package lookup finds when the registration of a domain ends, wherever its
// registry tells it: RDAP, with the servers of IANA's bootstrap file and
// those it leaves out; WHOIS, with the server and the form of query each
// registry wants; the web lookups of registries that have neither; and,
// where nothing answers, the delegation of the name in its parent zone,
// which tells at least whether it is registered. The answers are read by
// the parsers of package parse. It follows Whoice
// (github.com/XMZO/whoice), whose lists of servers it carries.
package lookup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/parse"
)

// errSkip means a source has no service for the name.
var errSkip = errors.New("no service for this name")

// ErrDomain means the name looked up is no domain below a public suffix.
var ErrDomain = errors.New("no domain")

// dnsFirst are suffixes whose registry's WHOIS cannot be had from here, so
// that only the delegation tells anything: whois.nic.org.uy takes no
// connections, whois.nic.es answers only addresses Red.es registered.
var dnsFirst = []string{"uy", "es"}

// Result is what a lookup found of a domain.
type Result struct {
	// Name is the registered domain looked up, in ASCII.
	Name   string
	Status model.ResultStatus
	// Expires is when the registration ends; zero when no answer tells.
	Expires   time.Time
	Registrar string
	// Source and Server are where the end, or else the status, came from.
	Source model.SourceName
	Server string
	// Note says why there is no end, as the registry or the lookup tells.
	Note string
	// Failed is set when nothing answered as it should, as opposed to a
	// registry that tells no end: worth asking again soon.
	Failed bool
}

// Lookup looks domains up.
type Lookup struct {
	rdap         *rdap
	whoisServers whoisServers
	whoisQuery   func(ctx context.Context, server, query string) (string, error)
	webModules   []webModule
	webClient    *http.Client
	delegation   delegation
	parsers      *parse.Registry
}

// New returns a Lookup that asks registries over HTTP with client, whose
// transport it sets up for them (see forRegistries), fetching missing
// intermediates with fetch.
func New(client, fetch *http.Client) *Lookup {
	if t, ok := client.Transport.(*http.Transport); ok {
		forRegistries(t, fetch)
	}
	return &Lookup{
		rdap:         newRDAP(client, time.Now),
		whoisServers: loadWhoisServers(),
		whoisQuery:   whoisQuery,
		webModules:   webModules,
		webClient:    client,
		delegation:   newDelegation(),
		parsers:      parse.Default(),
	}
}

// Domain looks up the registration of name, a domain or a name below one.
func (l *Lookup) Domain(ctx context.Context, name string) (Result, error) {
	q, err := normalize(name)
	if err != nil {
		return Result{}, err
	}
	sources := []func(context.Context, model.NormalizedQuery) (*model.RawResponse, error){l.lookupRDAP, l.whois, l.web}
	skippedWhois := firstDNS(q.Suffix)
	if skippedWhois {
		sources[1] = func(context.Context, model.NormalizedQuery) (*model.RawResponse, error) { return nil, errSkip }
	}
	type answer struct {
		raw *model.RawResponse
		err error
	}
	answers := make([]answer, len(sources))
	var wg sync.WaitGroup
	for i, source := range sources {
		wg.Go(func() {
			raw, err := source(ctx, q)
			answers[i] = answer{raw, err}
		})
	}
	wg.Wait()

	r := Result{Name: q.Query}
	var errs []string
	served := false
	for _, a := range answers {
		if errors.Is(a.err, errSkip) {
			continue
		}
		served = true
		if a.err != nil {
			errs = append(errs, a.err.Error())
			continue
		}
		part, err := l.parsers.Parse(ctx, *a.raw, q)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s %s: %v", a.raw.Source, hostOf(a.raw.Server), err))
			continue
		}
		r.add(part, a.raw)
	}
	if !r.Expires.IsZero() {
		return r, nil
	}

	// No end: the delegation tells at least whether the name is registered,
	// where nothing else did.
	switch {
	case !served && skippedWhois:
		r.Note = fmt.Sprintf("the WHOIS of .%s cannot be asked from here, and it has no RDAP", tld(q.Suffix))
	case !served:
		r.Note = fmt.Sprintf("the registry of .%s publishes no WHOIS or RDAP service", tld(q.Suffix))
	case r.Status == model.StatusUnregistered:
		r.Note = "the registry has no such name"
	case r.Status != "":
		r.Note = "the registry tells no expiration date"
	default:
		r.Note, r.Failed = strings.Join(errs, "; "), true
	}
	if r.Status == "" || r.Status == model.StatusUnknown {
		d, err := l.delegation.check(ctx, q.RegisteredDomain)
		switch {
		case err != nil && !served:
			return Result{}, fmt.Errorf("%s, and the DNS delegation could not be had: %w", r.Note, err)
		case err == nil && d.status != model.StatusUnknown:
			r.Status, r.Source, r.Server, r.Failed = d.status, model.SourceDNS, d.server, false
			if d.status == model.StatusRegistered {
				r.Note += fmt.Sprintf("; the %s zone delegates it", d.zone)
			} else {
				r.Note += fmt.Sprintf("; the %s zone has no such name", d.zone)
			}
		}
	}
	if r.Status == "" {
		r.Status = model.StatusUnknown
	}
	return r, nil
}

// add takes from part what r lacks: the end, the registrar, the status, in
// the order of the sources (RDAP, WHOIS, the web).
func (r *Result) add(part *model.PartialResult, raw *model.RawResponse) {
	if r.Expires.IsZero() {
		if t, ok := parse.Time(part.Dates.ExpiresAt); ok {
			r.Expires, r.Source, r.Server = t, raw.Source, raw.Server
		}
	}
	if r.Registrar == "" {
		r.Registrar = strings.TrimSpace(part.Registrar.Name)
	}
	if (r.Status == "" || r.Status == model.StatusUnknown) && part.Status != "" && part.Status != model.StatusUnknown {
		r.Status = part.Status
		if r.Source == "" {
			r.Source, r.Server = raw.Source, raw.Server
		}
	}
}

func (l *Lookup) lookupRDAP(ctx context.Context, q model.NormalizedQuery) (*model.RawResponse, error) {
	return l.rdap.lookup(ctx, q)
}

// normalize reads name: in ASCII and lower case, with its public suffix
// and the domain registered under it, which is what is looked up.
func normalize(name string) (model.NormalizedQuery, error) {
	ascii, err := idna.Lookup.ToASCII(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if err != nil {
		return model.NormalizedQuery{}, fmt.Errorf("%w: %q: %w", ErrDomain, name, err)
	}
	ascii = strings.ToLower(ascii)
	registered, err := publicsuffix.EffectiveTLDPlusOne(ascii)
	if err != nil {
		return model.NormalizedQuery{}, fmt.Errorf("%w: %q: %w", ErrDomain, name, err)
	}
	suffix, icann := publicsuffix.PublicSuffix(registered)
	q := model.NormalizedQuery{
		Input:            name,
		Query:            registered,
		Type:             model.QueryDomain,
		Host:             ascii,
		Suffix:           suffix,
		RegisteredDomain: registered,
		// Unlisted TLDs come out as not ICANN's too, but with one label.
		PrivateSuffix: !icann && strings.Contains(suffix, "."),
	}
	if u, err := idna.Lookup.ToUnicode(registered); err == nil && u != registered {
		q.UnicodeQuery = u
	}
	return q, nil
}

// firstDNS reports whether suffix is one of dnsFirst or below one.
func firstDNS(suffix string) bool {
	for _, s := range dnsFirst {
		if suffix == s || strings.HasSuffix(suffix, "."+s) {
			return true
		}
	}
	return false
}

func tld(suffix string) string {
	return suffix[strings.LastIndexByte(suffix, '.')+1:]
}

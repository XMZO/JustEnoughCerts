package parse

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// WHOISParser reads any WHOIS or WHOIS Web answer; the TLD parsers start from what it reads.
type WHOISParser struct{}

var unregisteredPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bno match\b`),
	// CONAC (.xn--zfr164b, .xn--55qw42g).
	regexp.MustCompile(`(?i)\bnot find matching ?record\b`),
	regexp.MustCompile(`(?i)\bnot found\b`),
	regexp.MustCompile(`(?i)\bno data found\b`),
	regexp.MustCompile(`(?i)\bdomain not found\b`),
	regexp.MustCompile(`(?i)\bstatus:\s*free\b`),
	regexp.MustCompile(`(?i)\bstatus:\s*available\b`),
	regexp.MustCompile(`(?i)\bno entries found\b`),
	regexp.MustCompile(`(?i)\bno information\b`),
	regexp.MustCompile(`(?i)\bno record found\b`),
	regexp.MustCompile(`(?i)\bno such domain\b`),
	regexp.MustCompile(`(?i)\bnot registered\b`),
	regexp.MustCompile(`(?i)\bhas not been registered\b`),
	regexp.MustCompile(`(?i)\bdomain (?:name )?is available\b`),
	regexp.MustCompile(`(?i)\bdomain unknown\b`),
	regexp.MustCompile(`(?i)\bobject_not_found\b`),
	regexp.MustCompile(`(?i)\bis available for registration\b`),
	// puntoGAL and other registries run on CORE's or the .uk registry's back end:
	// "The queried object does not exist: no matching objects found".
	regexp.MustCompile(`(?i)\bqueried object does not exist\b`),
	regexp.MustCompile(`(?i)\bno matching objects? found\b`),
	// PNINA (.ps): "Domain Status: No Object Found".
	regexp.MustCompile(`(?i)\bno object found\b`),
	regexp.MustCompile(`(?i)\bobject does not exist\b`),      // .by
	regexp.MustCompile(`(?im)^\s*no matching record\.?\s*$`), // .cn
	regexp.MustCompile(`(?im)^\s*no found\s*$`),              // .tw
	regexp.MustCompile(`(?im)^\s*\S+\.\S+ is free\s*$`),      // .nl: "example.nl is free"
	regexp.MustCompile(`(?i)\bis available for purchase\b`),  // .tm
	regexp.MustCompile(`(?i)\bno se encuentra registrado\b`), // .ar
	regexp.MustCompile(`(?i)\bno data was found to match\b`), // .il
}

var reservedRegistrarPattern = regexp.MustCompile(`(?i)^\s*(?:nic|registry|tld)?\s*reserved(?:\s+names?)?\s*$`)

var reservedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\breserved by (?:the )?registry\b`),
	regexp.MustCompile(`(?i)\breserved domain\b`),
	regexp.MustCompile(`(?i)\breserved domain name\b`),
	regexp.MustCompile(`(?i)\bdomain name is reserved\b`),
	regexp.MustCompile(`(?i)\bdomain (?:name )?(?:is )?reserved\b`),
	regexp.MustCompile(`(?i)\bhas been reserved\b`),
	regexp.MustCompile(`(?i)\breserved word\b`),
	regexp.MustCompile(`(?i)\bstatus:\s*not allowed\b`),
	regexp.MustCompile(`(?i)\bstatus:\s*forbidden\b`),
	regexp.MustCompile(`(?i)\bstatus:\s*prohibited\b`),
	regexp.MustCompile(`(?i)\bdomain(?: name)? is not allowed\b`),
	regexp.MustCompile(`(?i)\bname is restricted\b`),
	regexp.MustCompile(`(?i)\bcannot be registered\b`),
	regexp.MustCompile(`(?i)\bcan ?not be registered\b`),
	// Tucows Registry back ends (.بحرين, the Indian IDN TLDs, ...).
	regexp.MustCompile(`(?i)\bmatches a restricted word\b`),
	// SGNIC error 01044.
	regexp.MustCompile(`(?i)\bhas usage restrictions applied\b`),
	// HKIRC (.xn--j6w193g) for names withheld from registration.
	regexp.MustCompile(`(?i)\bcurrently not available for registration\b`),
	// cctld.by (.бел): released names waiting for auction.
	regexp.MustCompile(`(?i)\bobject pending auction\b`),
}

// rejectionPatterns are replies that refuse the query itself: the server does
// not serve the TLD, or the name is not valid there. They carry no record, so
// the status is left to the other sources and the DNS delegation check.
var rejectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\btld (?:is )?not supported\b`),
	regexp.MustCompile(`(?i)\bregistration status:\s*invalid\b`),
	regexp.MustCompile(`(?im)^\s*-\d+:\s*%invalid pattern\b`),
	regexp.MustCompile(`(?i)\berreur\s+syntax\b`),
}

// Name returns the parser's name.
func (WHOISParser) Name() string { return "whois-generic" }

// Supports reports whether the parser reads raw.
func (WHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS || raw.Source == model.SourceWHOISWeb
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (WHOISParser) Priority() int { return 50 }

// Parse reads the answer.
func (WHOISParser) Parse(_ context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial := &model.PartialResult{
		Source: raw.Source,
		Status: model.StatusRegistered,
		Raw:    rawDataForWHOISSource(raw),
		Domain: model.DomainInfo{
			Name:             q.Query,
			PunycodeName:     q.Query,
			UnicodeName:      q.UnicodeQuery,
			Suffix:           q.Suffix,
			RegisteredDomain: q.RegisteredDomain,
		},
	}

	if raw.Body == "" {
		partial.Status = model.StatusUnknown
		partial.Warnings = append(partial.Warnings, "empty WHOIS response")
		return partial, nil
	}
	if raw.Source == model.SourceWHOISWeb && isWHOISWebNotice(raw.Body) {
		partial.Status = model.StatusUnknown
		partial.Warnings = append(partial.Warnings, "WHOIS Web fallback requires manual lookup")
		return partial, nil
	}
	if pattern := firstMatch(rejectionPatterns, raw.Body); pattern != "" {
		return nil, fmt.Errorf("%s rejected the query: %s", raw.Server, pattern)
	}
	// A reply that names the domain restricted or reserved also tends to say
	// the object does not exist (ZDNS: "The queried object does not exist:
	// ... This domain name is restricted from registration"); reserved wins.
	if matchesAny(reservedPatterns, raw.Body) {
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
	} else if matchesAny(unregisteredPatterns, raw.Body) {
		partial.Status = model.StatusUnregistered
	}

	fields := parseKeyValues(raw.Body)
	// A short reply with no fields and no known phrasing ("CMM174" from a
	// throttled HKIRC) is not a record; reading it as one made every name
	// "registered".
	if partial.Status == model.StatusRegistered && len(fields) == 0 && len(strings.TrimSpace(raw.Body)) < 200 {
		return nil, fmt.Errorf("unrecognized WHOIS reply from %s: %q", raw.Server, strings.TrimSpace(raw.Body))
	}

	partial.Domain.Name = firstValue(fields, "domain name", "domain")
	if partial.Domain.Name == "" && q.Type == model.QueryDomain {
		partial.Domain.Name = q.Query
	}
	partial.Domain.PunycodeName = partial.Domain.Name
	partial.Domain.UnicodeName = q.UnicodeQuery
	partial.Domain.Suffix = q.Suffix
	partial.Domain.RegisteredDomain = q.RegisteredDomain
	partial.Domain.Registered = partial.Status == model.StatusRegistered && q.Type == model.QueryDomain

	partial.Registry.WHOISServer = firstValue(fields, "whois", "whois server", "registry whois server")
	partial.Registrar.Name = firstValue(fields, "registrar", "sponsoring registrar", "registrar name", "registrar-name")
	partial.Registrar.URL = normalizeRegistrarURL(firstValue(fields, "registrar url", "sponsoring registrar url", "registrar website", "registrar-url", "registration service url"))
	if registrarName, registrarURL := splitRegistrarURL(partial.Registrar.Name); registrarURL != "" && partial.Registrar.URL == "" {
		partial.Registrar.Name = registrarName
		partial.Registrar.URL = registrarURL
	}
	// Some registries park reserved names under a placeholder registrar
	// ("Registrar: NIC Reserved" on whois.nic.cd for ac.cd, com.cd, ...).
	if reservedRegistrarPattern.MatchString(partial.Registrar.Name) && partial.Status != model.StatusUnregistered {
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
	}
	partial.Registrar.IANAID = firstValue(fields, "registrar iana id", "iana id")
	partial.Registrar.Email = firstValue(fields, "registrar email", "registrar contact email")
	partial.Registrar.Phone = firstValue(fields, "registrar phone", "registrar contact phone")
	partial.Registrar.Address = joinNonEmpty(", ",
		firstValue(fields, "registrar street address", "registrar street", "registrar address"),
		firstValue(fields, "registrar city"),
		firstValue(fields, "registrar state", "registrar state/province"),
		firstValue(fields, "registrar postal code"))
	partial.Registrar.Country = firstValue(fields, "registrar country")
	partial.Registrar.WHOISServer = firstValue(fields, "registrar whois server")
	partial.Dates.CreatedAt = firstValue(fields, "creation date", "created", "created on", "registered on")
	partial.Dates.ExpiresAt = firstValue(fields, "registry expiry date", "expiration date", "expiry date", "expires", "paid-till")
	partial.Dates.UpdatedAt = firstValue(fields, "updated date", "last updated", "last updated on", "updated on", "modified", "changed")
	partial.DNSSEC.Text = firstValue(fields, "dnssec")

	for _, status := range valuesFor(fields, "domain status", "status") {
		code := strings.Fields(status)
		if len(code) == 0 {
			continue
		}
		partial.Statuses = append(partial.Statuses, model.DomainStatus{
			Code:   code[0],
			Label:  code[0],
			Source: string(model.SourceWHOIS),
		})
	}

	for _, ns := range valuesFor(fields, "name server", "nserver", "nameserver") {
		nsFields := strings.Fields(ns)
		if len(nsFields) == 0 {
			continue
		}
		host := strings.ToLower(strings.TrimSuffix(nsFields[0], "."))
		if host != "" && !isPlaceholderNameserver(host) && !hasNameserver(partial.Nameservers, host) {
			partial.Nameservers = append(partial.Nameservers, model.Nameserver{Host: host})
		}
	}

	partial.Registrant.Name = firstValue(fields, "registrant name", "registrant", "owner name")
	partial.Registrant.Organization = firstValue(fields, "registrant organization", "registrant org", "org", "organization", "owner organization")
	partial.Registrant.Country = firstValue(fields, "registrant country", "owner country")
	partial.Registrant.Province = firstValue(fields, "registrant state/province", "registrant province", "registrant state", "owner state", "owner province")
	partial.Registrant.City = firstValue(fields, "registrant city", "owner city")
	partial.Registrant.Address = firstValue(fields, "registrant street", "registrant address", "owner address")
	partial.Registrant.PostalCode = firstValue(fields, "registrant postal code", "registrant zip", "owner postal code")
	partial.Registrant.Email = contactEmail(firstValue(fields, "registrant email", "registrant contact email", "owner email", "email"))
	partial.Registrant.Phone = contactPhone(firstValue(fields, "registrant phone", "registrant phone number", "owner phone", "phone"))

	partial.Network.CIDR = firstValue(fields, "cidr")
	partial.Network.Range = firstValue(fields, "netrange", "inetnum", "inet6num")
	partial.Network.Name = firstValue(fields, "netname", "network name")
	partial.Network.Type = firstValue(fields, "nettype")
	partial.Network.OriginAS = firstValue(fields, "originas", "origin as") //nolint:misspell // "originas" is the OriginAS key of RIR WHOIS, lowercased
	partial.Network.Country = firstValue(fields, "country")

	// A miss can still carry a "Domain Status:" line ("No Object Found");
	// it is not an EPP status of a registration.
	if partial.Status == model.StatusUnregistered {
		partial.Statuses = nil
	}

	return partial, nil
}

func parseKeyValues(body string) map[string][]string {
	fields := map[string][]string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">>>") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		fields[key] = append(fields[key], value)
	}
	return fields
}

func firstValue(fields map[string][]string, keys ...string) string {
	for _, key := range keys {
		values := fields[strings.ToLower(key)]
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func valuesFor(fields map[string][]string, keys ...string) []string {
	var values []string
	for _, key := range keys {
		values = append(values, fields[strings.ToLower(key)]...)
	}
	return values
}

// firstMatch returns the text the first matching pattern found, trimmed.
func firstMatch(patterns []*regexp.Regexp, body string) string {
	for _, pattern := range patterns {
		if match := pattern.FindString(body); match != "" {
			return strings.TrimSpace(match)
		}
	}
	return ""
}

func matchesAny(patterns []*regexp.Regexp, body string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(body) {
			return true
		}
	}
	return false
}

func hasNameserver(values []model.Nameserver, host string) bool {
	for _, value := range values {
		if value.Host == host {
			return true
		}
	}
	return false
}

func rawDataForWHOISSource(raw model.RawResponse) model.RawData {
	if raw.Source == model.SourceWHOISWeb {
		return model.RawData{WHOISWeb: raw.Body}
	}
	return model.RawData{WHOIS: raw.Body}
}

func isWHOISWebNotice(body string) bool {
	body = strings.TrimSpace(strings.ToLower(body))
	return strings.HasPrefix(body, "whois web fallback notice:") ||
		strings.HasPrefix(body, "please visit ")
}

var registrarURLPattern = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9.-]*\.[a-z0-9-]{2,}(?::\d+)?(?:/.*)?$`)

func normalizeRegistrarURL(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, " \t\r\n<>[]()'\"")
	if value == "" {
		return ""
	}
	if extracted := firstURL(value); extracted != "" {
		return extracted
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	if registrarURLPattern.MatchString(value) {
		return "http://" + value
	}
	return ""
}

func splitRegistrarURL(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	left, rest, ok := strings.Cut(value, "(")
	if !ok {
		return value, ""
	}
	candidate, _, ok := strings.Cut(rest, ")")
	if !ok {
		return value, ""
	}
	url := normalizeRegistrarURL(candidate)
	if url == "" {
		return value, ""
	}
	name := strings.TrimSpace(left)
	if name == "" {
		name = value
	}
	return name, url
}

// contactEmail keeps addresses and contact-form links. Registries that redact
// the field often put a sentence there instead ("Please query the Whois
// service of the Registrar of Record ..."), which is not contact data.
func contactEmail(value string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if strings.Contains(value, "@") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return value
	}
	return ""
}

// contactPhone drops placeholders such as "+000000000" that some registrars
// put in redacted records.
func contactPhone(value string) string {
	value = strings.TrimSpace(value)
	digits := strings.Trim(strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, value), "0")
	if digits == "" {
		return ""
	}
	return value
}

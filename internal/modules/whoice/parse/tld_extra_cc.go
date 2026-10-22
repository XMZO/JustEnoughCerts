package parse

import (
	"context"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// MXWHOISParser reads the WHOIS answers for .mx.
type MXWHOISParser struct{}

// Name returns the parser's name.
func (MXWHOISParser) Name() string { return "whois-tld-mx" }

// Supports reports whether the parser reads raw.
func (MXWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".mx")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (MXWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (MXWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "last updated on"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, normalizeRegistrarURL(firstValue(fields, "url", "registrar url")))
	if nameservers := nameserversFromValues(valuesFor(fields, "dns", "name server", "nameserver", "nserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	registrantFields := parseFlexibleKeyValues(strings.Join(blockLinesFlexible(raw.Body, "registrant"), "\n"))
	partial.Registrant.Name = pickString(partial.Registrant.Name, firstValue(registrantFields, "name"))
	partial.Registrant.Organization = pickString(partial.Registrant.Organization, firstValue(registrantFields, "organization"))
	partial.Registrant.Country = pickString(partial.Registrant.Country, firstValue(registrantFields, "country"))
	partial.Registrant.Province = pickString(partial.Registrant.Province, firstValue(registrantFields, "state", "state/province"))
	partial.Registrant.City = pickString(partial.Registrant.City, firstValue(registrantFields, "city"))
	partial.Registrant.Address = pickString(partial.Registrant.Address, firstValue(registrantFields, "address", "street"))
	partial.Registrant.PostalCode = pickString(partial.Registrant.PostalCode, firstValue(registrantFields, "postal code", "zipcode", "zip code"))
	return partial, nil
}

// PTWHOISParser reads the WHOIS answers for .pt.
type PTWHOISParser struct{}

// Name returns the parser's name.
func (PTWHOISParser) Name() string { return "whois-tld-pt" }

// Supports reports whether the parser reads raw.
func (PTWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".pt")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (PTWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (PTWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	if domain := firstValue(fields, "domain"); domain != "" {
		setDomainName(partial, domain)
	}
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registrar name", "registrar"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "creation date", "created"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expiration date", "expires"))
	if statuses := statusesFromValues(valuesFor(fields, "domain status", "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromValues(valuesFor(fields, "name server", "nameserver", "nserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	partial.Registrant.Name = pickString(partial.Registrant.Name, firstValue(fields, "owner name", "holder name", "registrant name"))
	partial.Registrant.Country = pickString(partial.Registrant.Country, firstValue(fields, "owner country code", "owner country", "registrant country"))
	partial.Registrant.City = pickString(partial.Registrant.City, firstValue(fields, "owner locality", "owner city", "registrant city"))
	partial.Registrant.Address = pickString(partial.Registrant.Address, firstValue(fields, "owner address", "registrant address"))
	partial.Registrant.PostalCode = pickString(partial.Registrant.PostalCode, firstValue(fields, "owner zipcode", "owner zip code", "owner postal code", "registrant postal code"))
	return partial, nil
}

// QAWHOISParser reads the WHOIS answers for .qa.
type QAWHOISParser struct{}

// Name returns the parser's name.
func (QAWHOISParser) Name() string { return "whois-tld-qa" }

// Supports reports whether the parser reads raw.
func (QAWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".qa")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (QAWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (QAWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	body := strings.ToLower(raw.Body)
	if strings.Contains(body, "reserved by qdr") || strings.Contains(body, "is not available") {
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
		partial.Domain.Registered = false
		return partial, nil
	}
	fields := parseFlexibleKeyValues(raw.Body)
	if statuses := statusesFromValues(valuesFor(fields, "domain status", "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromValues(valuesFor(fields, "name server", "nameserver", "nserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// MDWHOISParser reads the WHOIS answers for .md.
type MDWHOISParser struct{}

// Name returns the parser's name.
func (MDWHOISParser) Name() string { return "whois-tld-md" }

// Supports reports whether the parser reads raw.
func (MDWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".md")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (MDWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (MDWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expires on", "expire on", "expired on"))
	if statuses := statusesFromWhitespace(valuesFor(fields, "domain status", "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromValues(valuesFor(fields, "name server", "nameserver", "nserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// LUWHOISParser reads the WHOIS answers for .lu.
type LUWHOISParser struct{}

// Name returns the parser's name.
func (LUWHOISParser) Name() string { return "whois-tld-lu" }

// Supports reports whether the parser reads raw.
func (LUWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".lu")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (LUWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (LUWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	domainType := firstValue(fields, "domaintype", "domain type")
	if strings.EqualFold(strings.TrimSpace(domainType), "reserved") {
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
		partial.Domain.Registered = false
		return partial, nil
	}
	if domainType != "" {
		partial.Statuses = luStatuses(domainType, raw.Source)
	}
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registrar-name", "registrar name", "registrar"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, normalizeRegistrarURL(firstValue(fields, "registrar-url", "registrar url")))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "registered", "created"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "modified", "changed"))
	if nameservers := nameserversFromValues(valuesFor(fields, "nserver", "name server", "nameserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// LVWHOISParser reads the WHOIS answers for .lv.
type LVWHOISParser struct{}

// Name returns the parser's name.
func (LVWHOISParser) Name() string { return "whois-tld-lv" }

// Supports reports whether the parser reads raw.
func (LVWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".lv")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (LVWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (LVWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	domainFields := parseKeyValues(strings.Join(bracketSectionLines(raw.Body, "domain"), "\n"))
	if domain := firstValue(domainFields, "domain", "domain name"); domain != "" {
		setDomainName(partial, domain)
	}
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(domainFields, "registered", "created"))
	if statuses := statusesFromValues(valuesFor(domainFields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}

	holderFields := parseKeyValues(strings.Join(bracketSectionLines(raw.Body, "holder"), "\n"))
	partial.Registrant.Name = pickString(partial.Registrant.Name, firstValue(holderFields, "name"))
	partial.Registrant.Organization = pickString(partial.Registrant.Organization, firstValue(holderFields, "organization"))
	partial.Registrant.Country = pickString(partial.Registrant.Country, firstValue(holderFields, "country", "country code"))
	partial.Registrant.Address = pickString(partial.Registrant.Address, firstValue(holderFields, "address"))
	partial.Registrant.Email = pickString(partial.Registrant.Email, firstValue(holderFields, "email"))
	partial.Registrant.Phone = pickString(partial.Registrant.Phone, firstValue(holderFields, "phone"))

	registrarFields := parseKeyValues(strings.Join(bracketSectionLines(raw.Body, "registrar"), "\n"))
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(registrarFields, "name"))
	if nameservers := nameserversFromValues(valuesFor(parseKeyValues(strings.Join(bracketSectionLines(raw.Body, "nservers"), "\n")), "nserver", "name server", "nameserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// AMWHOISParser reads the WHOIS answers for .am.
type AMWHOISParser struct{}

// Name returns the parser's name.
func (AMWHOISParser) Name() string { return "whois-tld-am" }

// Supports reports whether the parser reads raw.
func (AMWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".am")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (AMWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (AMWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "reserved name") {
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
		partial.Domain.Registered = false
		return partial, nil
	}
	fields := parseFlexibleKeyValues(raw.Body)
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "registered"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "last modified"))
	if statuses := statusesFromValues(valuesFor(fields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromBlock(amDNSBlock(raw.Body)); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	if strings.Contains(strings.ToLower(raw.Body), "dns servers (zone signed") {
		partial.DNSSEC.Text = "signed"
		signed := true
		partial.DNSSEC.Signed = &signed
	}
	return partial, nil
}

// AXWHOISParser reads the WHOIS answers for .ax.
type AXWHOISParser struct{}

// Name returns the parser's name.
func (AXWHOISParser) Name() string { return "whois-tld-ax" }

// Supports reports whether the parser reads raw.
func (AXWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".ax")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (AXWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (AXWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	partial.Registrar.URL = pickString(partial.Registrar.URL, normalizeRegistrarURL(firstValue(fields, "www")))
	return partial, nil
}

// BDWHOISParser reads the WHOIS answers for .bd.
type BDWHOISParser struct{}

// Name returns the parser's name.
func (BDWHOISParser) Name() string { return "whois-tld-bd" }

// Supports reports whether the parser reads raw.
func (BDWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".bd")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (BDWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (BDWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	if statuses := statusesFromValues(valuesFor(fields, "domain status", "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromValues(valuesFor(fields, "primary dns", "secondary dns", "tertiary dns", "name server", "nameserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	// whois.get.bd writes day/month/year ("Activation Date: 18/10/2003").
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, dayMonthYear(firstValue(fields, "activation date")))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, dayMonthYear(firstValue(fields, "expiry date")))
	partial.Registrant.Name = pickString(partial.Registrant.Name, firstValue(fields, "registrant's name"))
	return partial, nil
}

// dayMonthYear converts "18/10/2003" or "18-10-2003" to "2003-10-18";
// other values pass through unchanged.
func dayMonthYear(value string) string {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"02/01/2006", "02-01-2006"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	return value
}

// BNWHOISParser reads the WHOIS answers for .bn.
type BNWHOISParser struct{}

// Name returns the parser's name.
func (BNWHOISParser) Name() string { return "whois-tld-bn" }

// Supports reports whether the parser reads raw.
func (BNWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".bn")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (BNWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (BNWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if nameservers := nameserversFromBlock(blockLinesFlexible(raw.Body, "name servers")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// GGWHOISParser reads the WHOIS answers for .gg and .je.
type GGWHOISParser struct{}

// Name returns the parser's name.
func (GGWHOISParser) Name() string { return "whois-tld-gg-je" }

// Supports reports whether the parser reads raw.
func (GGWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".gg", ".je")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (GGWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (GGWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, lineValueAfter(raw.Body, "registered on"))
	if statuses := fullStatusLinesFromBlock(blockLinesFlexible(raw.Body, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromBlock(blockLinesFlexible(raw.Body, "name servers")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// ILWHOISParser reads the WHOIS answers for .il.
type ILWHOISParser struct{}

// Name returns the parser's name.
func (ILWHOISParser) Name() string { return "whois-tld-il" }

// Supports reports whether the parser reads raw.
func (ILWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".il")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (ILWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (ILWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registrar name"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, normalizeRegistrarURL(firstValue(fields, "registrar info")))
	// ISOC-IL writes day-month-year: "assigned: 05-06-1999".
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, dayMonthYear(firstValue(fields, "assigned")))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, dayMonthYear(firstValue(fields, "validity")))
	if changed := ilLastChangedDate(raw.Body); changed != "" {
		partial.Dates.UpdatedAt = changed
	}
	return partial, nil
}

// LTWHOISParser reads the WHOIS answers for .lt.
type LTWHOISParser struct{}

// Name returns the parser's name.
func (LTWHOISParser) Name() string { return "whois-tld-lt" }

// Supports reports whether the parser reads raw.
func (LTWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".lt")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (LTWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (LTWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	status := strings.ToLower(firstValue(fields, "status"))
	switch status {
	case "available":
		markUnregistered(partial)
		return partial, nil
	case "blocked":
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
		partial.Domain.Registered = false
		return partial, nil
	}
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "registered"))
	return partial, nil
}

// MOWHOISParser reads the WHOIS answers for .mo.
type MOWHOISParser struct{}

// Name returns the parser's name.
func (MOWHOISParser) Name() string { return "whois-tld-mo" }

// Supports reports whether the parser reads raw.
func (MOWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".mo")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (MOWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (MOWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "record created on", "created"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, lineValueAfter(raw.Body, "record created on"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "record expires on", "expires"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, lineValueAfter(raw.Body, "record expires on"))
	if nameservers := nameserversFromBlock(moNameserverBlock(raw.Body)); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// RSWHOISParser reads the WHOIS answers for .rs.
type RSWHOISParser struct{}

// Name returns the parser's name.
func (RSWHOISParser) Name() string { return "whois-tld-rs" }

// Supports reports whether the parser reads raw.
func (RSWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".rs")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (RSWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (RSWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "registration date", "registered"))
	if nameservers := nameserversFromValues(valuesFor(fields, "dns", "name server", "nameserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// STWHOISParser reads the WHOIS answers for .st.
type STWHOISParser struct{}

// Name returns the parser's name.
func (STWHOISParser) Name() string { return "whois-tld-st" }

// Supports reports whether the parser reads raw.
func (STWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".st")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (STWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (STWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	partial.Registrar.URL = pickString(partial.Registrar.URL, normalizeRegistrarURL(firstValue(fields, "url")))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "created-date"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expiration-date"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "updated-date"))
	if statuses := statusesFromValues(valuesFor(fields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	return partial, nil
}

// UZWHOISParser reads the WHOIS answers for .uz.
type UZWHOISParser struct{}

// Name returns the parser's name.
func (UZWHOISParser) Name() string { return "whois-tld-uz" }

// Supports reports whether the parser reads raw.
func (UZWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".uz", ".co.uz", ".com.uz", ".net.uz", ".org.uz")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (UZWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (UZWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	if nameservers := nameserversFromValues(filterDefinedNameservers(valuesFor(fields, "name server", "nameserver"))); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

func statusesFromWhitespace(values []string, source model.SourceName) []model.DomainStatus {
	var out []model.DomainStatus
	for _, value := range values {
		for _, item := range strings.Fields(value) {
			out = appendStatus(out, item, source)
		}
	}
	return out
}

func fullStatusLinesFromBlock(lines []string, source model.SourceName) []model.DomainStatus {
	var out []model.DomainStatus
	for _, line := range lines {
		code := strings.TrimSpace(line)
		if code == "" {
			continue
		}
		out = appendStatus(out, code, source)
		if len(out) > 0 {
			out[len(out)-1].Code = code
			out[len(out)-1].Label = code
		}
	}
	return out
}

func amDNSBlock(body string) []string {
	lines := strings.Split(normalizeNewlines(body), "\n")
	for i, line := range lines {
		key, value, ok := flexibleHeadingLine(line)
		if !ok || !strings.HasPrefix(key, "dns servers") {
			continue
		}
		var out []string
		if value != "" {
			out = append(out, value)
		}
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == "" {
				if len(out) > 0 {
					break
				}
				continue
			}
			if len(out) > 0 && isTopLevelBlockHeading(lines[j]) {
				break
			}
			out = append(out, trimmed)
		}
		return out
	}
	return nil
}

func moNameserverBlock(body string) []string {
	lines := blockLinesFlexible(body, "domain name servers")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Trim(trimmed, "-") == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func lineValueAfter(body, prefix string) string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	for _, line := range strings.Split(normalizeNewlines(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			continue
		}
		return strings.TrimSpace(trimmed[len(prefix):])
	}
	return ""
}

func ilLastChangedDate(body string) string {
	var last string
	for _, line := range strings.Split(normalizeNewlines(body), "\n") {
		key, value, ok := headingLine(line)
		if !ok || key != "changed" || !strings.Contains(strings.ToLower(value), "(changed)") {
			continue
		}
		for _, token := range strings.Fields(value) {
			token = strings.Trim(token, " \t\r\n()")
			if len(token) == 8 && allDigits(token) {
				last = token
			}
		}
	}
	return last
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func filterDefinedNameservers(values []string) []string {
	var out []string
	for _, value := range values {
		lower := strings.ToLower(strings.TrimSpace(value))
		if lower == "" || strings.Contains(lower, "not defined") || strings.Contains(lower, "not.defined") || strings.Contains(lower, "<no value>") {
			continue
		}
		out = append(out, value)
	}
	return out
}

func luStatuses(value string, source model.SourceName) []model.DomainStatus {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var out []model.DomainStatus
	mainValue, detail, hasDetail := strings.Cut(value, "(")
	out = appendStatus(out, strings.TrimSpace(mainValue), source)
	if hasDetail {
		out = appendStatus(out, strings.TrimSuffix(strings.TrimSpace(detail), ")"), source)
	}
	return out
}

func bracketSectionLines(body, section string) []string {
	section = strings.ToLower(strings.TrimSpace(section))
	var current string
	var out []string
	for _, line := range strings.Split(normalizeNewlines(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			current = strings.ToLower(strings.TrimSpace(strings.Trim(trimmed, "[]")))
			continue
		}
		if current == section {
			out = append(out, line)
		}
	}
	return out
}

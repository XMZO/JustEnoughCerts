package parse

import (
	"context"
	"regexp"
	"strings"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// DEWHOISParser reads the WHOIS answers for .de.
type DEWHOISParser struct{}

// Name returns the parser's name.
func (DEWHOISParser) Name() string { return "whois-tld-de" }

// Supports reports whether the parser reads raw.
func (DEWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".de")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (DEWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (DEWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "status: free") {
		markUnregistered(partial)
		return partial, nil
	}
	fields := parseFlexibleKeyValues(raw.Body)
	if domain := firstValue(fields, "domain"); domain != "" {
		setDomainName(partial, domain)
	}
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "changed"))
	if statuses := statusesFromValues(valuesFor(fields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	applyDNSSECText(partial, firstValue(fields, "dnssec"))
	return partial, nil
}

// NLWHOISParser reads the WHOIS answers for .nl.
type NLWHOISParser struct{}

// Name returns the parser's name.
func (NLWHOISParser) Name() string { return "whois-tld-nl" }

// Supports reports whether the parser reads raw.
func (NLWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".nl")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (NLWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (NLWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if isNLUnregistered(raw.Body) {
		markUnregistered(partial)
		return partial, nil
	}
	fields := parseKeyValues(raw.Body)
	if domain := firstValue(fields, "domain name"); domain != "" {
		setDomainName(partial, domain)
	}
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "creation date"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "updated date"))
	if registrar := firstPlainLine(blockUntilNextField(raw.Body, "registrar")); registrar != "" {
		partial.Registrar.Name = pickString(partial.Registrar.Name, registrar)
	}
	if nameservers := nameserversFromBlock(blockUntilNextField(raw.Body, "domain nameservers")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	if statuses := statusesFromValues(valuesFor(fields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	applyDNSSECText(partial, firstValue(fields, "dnssec"))
	return partial, nil
}

// CAWHOISParser reads the WHOIS answers for .ca.
type CAWHOISParser struct{}

// Name returns the parser's name.
func (CAWHOISParser) Name() string { return "whois-tld-ca" }

// Supports reports whether the parser reads raw.
func (CAWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".ca")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (CAWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (CAWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "not found:") {
		markUnregistered(partial)
		return partial, nil
	}
	fields := parseKeyValues(raw.Body)
	if statuses := statusesFromValues(valuesFor(fields, "domain status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expiry date", "registry expiry date"))
	applyDNSSECText(partial, firstValue(fields, "dnssec"))
	return partial, nil
}

// AUWHOISParser reads the WHOIS answers for .au.
type AUWHOISParser struct{}

// Name returns the parser's name.
func (AUWHOISParser) Name() string { return "whois-tld-au" }

// Supports reports whether the parser reads raw.
func (AUWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".au")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (AUWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (AUWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseKeyValues(raw.Body)
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registrar name"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "last modified"))
	if statuses := statusesFromValues(valuesFor(fields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	applyDNSSECText(partial, firstValue(fields, "dnssec"))
	return partial, nil
}

// SEWHOISParser reads the WHOIS answers for .se and .nu.
type SEWHOISParser struct{}

// Name returns the parser's name.
func (SEWHOISParser) Name() string { return "whois-tld-se-nu" }

// Supports reports whether the parser reads raw.
func (SEWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".se", ".nu")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (SEWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (SEWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	if domain := firstValue(fields, "domain"); domain != "" {
		setDomainName(partial, domain)
	}
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registrar"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "created"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "modified"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expires"))
	if statuses := statusesFromValues(valuesFor(fields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromValues(valuesFor(fields, "nserver", "name server", "nameserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	applyDNSSECText(partial, firstValue(fields, "dnssec", "signed delegation"))
	return partial, nil
}

// FIWHOISParser reads the WHOIS answers for .fi.
type FIWHOISParser struct{}

// Name returns the parser's name.
func (FIWHOISParser) Name() string { return "whois-tld-fi" }

// Supports reports whether the parser reads raw.
func (FIWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".fi")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (FIWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (FIWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseFlexibleKeyValues(raw.Body)
	if domain := firstValue(fields, "domain"); domain != "" {
		setDomainName(partial, domain)
	}
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registrar"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, firstValue(fields, "www"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "created"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "modified"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expires"))
	if statuses := statusesFromValues(valuesFor(fields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromValues(valuesFor(fields, "nserver", "name server", "nameserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	applyDNSSECText(partial, firstValue(fields, "dnssec"))
	return partial, nil
}

// KRWHOISParser reads the WHOIS answers for .kr.
type KRWHOISParser struct{}

// Name returns the parser's name.
func (KRWHOISParser) Name() string { return "whois-tld-kr" }

// Supports reports whether the parser reads raw.
func (KRWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".kr")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (KRWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (KRWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "name is restricted") {
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
		partial.Domain.Registered = false
		return partial, nil
	}
	fields := parseKeyValues(raw.Body)
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "authorized agency"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "registered date"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "last updated date"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expiration date"))
	if statuses := statusesFromValues(valuesFor(fields, "domain status", "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromValues(valuesFor(fields, "host name", "name server", "nameserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	applyDNSSECText(partial, firstValue(fields, "dnssec"))
	return partial, nil
}

// ATWHOISParser reads the WHOIS answers for .at.
type ATWHOISParser struct{}

// Name returns the parser's name.
func (ATWHOISParser) Name() string { return "whois-tld-at" }

// Supports reports whether the parser reads raw.
func (ATWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".at")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (ATWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (ATWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "nothing found") {
		markUnregistered(partial)
		return partial, nil
	}
	fields := parseKeyValues(raw.Body)
	if domain := firstValue(fields, "domain"); domain != "" {
		setDomainName(partial, domain)
	}
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registrar"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "changed"))
	if nameservers := nameserversFromValues(valuesFor(fields, "nserver", "name server", "nameserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	applyDNSSECText(partial, firstValue(fields, "dnssec"))
	return partial, nil
}

// RUWHOISParser reads the WHOIS answers for .ru, .su and .xn--p1ai.
type RUWHOISParser struct{}

// Name returns the parser's name.
func (RUWHOISParser) Name() string { return "whois-tld-ru-su" }

// Supports reports whether the parser reads raw.
func (RUWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".ru", ".su", ".xn--p1ai")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (RUWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (RUWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseKeyValues(raw.Body)
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "paid-till"))
	if statuses := statusesFromValues(valuesFor(fields, "state"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	return partial, nil
}

// EEWHOISParser reads the WHOIS answers for .ee.
type EEWHOISParser struct{}

// Name returns the parser's name.
func (EEWHOISParser) Name() string { return "whois-tld-ee" }

// Supports reports whether the parser reads raw.
func (EEWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".ee")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (EEWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (EEWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "domain not found") {
		markUnregistered(partial)
		return partial, nil
	}
	domainFields := parseKeyValues(strings.Join(blockLinesFlexible(raw.Body, "domain"), "\n"))
	if domain := firstValue(domainFields, "name"); domain != "" {
		setDomainName(partial, domain)
	}
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(domainFields, "registered"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(domainFields, "changed"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(domainFields, "expire"))
	if statuses := statusesFromValues(valuesFor(domainFields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	registrarFields := parseKeyValues(strings.Join(blockLinesFlexible(raw.Body, "registrar"), "\n"))
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(registrarFields, "name"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, firstValue(registrarFields, "url"))
	if nameservers := nameserversFromValues(valuesFor(parseKeyValues(strings.Join(blockLinesFlexible(raw.Body, "nameserver"), "\n")), "nserver", "name server", "nameserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	applyDNSSECText(partial, firstValue(domainFields, "dnssec"))
	return partial, nil
}

// BGWHOISParser reads the WHOIS answers for .bg.
type BGWHOISParser struct{}

// Name returns the parser's name.
func (BGWHOISParser) Name() string { return "whois-tld-bg" }

// Supports reports whether the parser reads raw.
func (BGWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".bg")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (BGWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (BGWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseKeyValues(raw.Body)
	if status := firstValue(fields, "registration status"); strings.EqualFold(status, "available") {
		markUnregistered(partial)
		return partial, nil
	}
	if statuses := statusesFromValues(valuesFor(fields, "registration status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromBlock(blockUntilNextField(raw.Body, "name server information")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	applyDNSSECText(partial, firstValue(fields, "dnssec"))
	return partial, nil
}

// KGWHOISParser reads the WHOIS answers for .kg.
type KGWHOISParser struct{}

var kgDomainLinePattern = regexp.MustCompile(`(?im)^domain\s+([^\s]+)\s+\(([^)]+)\)`)

// Name returns the parser's name.
func (KGWHOISParser) Name() string { return "whois-tld-kg" }

// Supports reports whether the parser reads raw.
func (KGWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".kg")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (KGWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (KGWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "domain is available for registration") {
		markUnregistered(partial)
		return partial, nil
	}
	if matches := kgDomainLinePattern.FindStringSubmatch(raw.Body); matches != nil {
		setDomainName(partial, matches[1])
		if statuses := statusesFromValues([]string{matches[2]}, raw.Source); len(statuses) > 0 {
			partial.Statuses = statuses
		}
	}
	fields := parseKeyValues(raw.Body)
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "domain support"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "record created"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "record last updated on"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "record expires on"))
	if nameservers := nameserversFromBlock(blockUntilNextField(raw.Body, "name servers in the listed order")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// TRWHOISParser reads the WHOIS answers for .tr.
type TRWHOISParser struct{}

// Name returns the parser's name.
func (TRWHOISParser) Name() string { return "whois-tld-tr" }

// Supports reports whether the parser reads raw.
func (TRWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".tr")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (TRWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (TRWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "no match found") {
		markUnregistered(partial)
		return partial, nil
	}
	fields := parseFlexibleKeyValues(raw.Body)
	if domain := firstValue(fields, "** domain name", "domain name"); domain != "" {
		setDomainName(partial, domain)
	}
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "organization name"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "created on"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expires on"))
	if nameservers := nameserversFromBlock(blockUntilNextField(raw.Body, "** domain servers")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// HKWHOISParser reads the WHOIS answers for .hk.
type HKWHOISParser struct{}

// Name returns the parser's name.
func (HKWHOISParser) Name() string { return "whois-tld-hk" }

// Supports reports whether the parser reads raw.
func (HKWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".hk")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (HKWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (HKWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "has not been registered") {
		markUnregistered(partial)
		return partial, nil
	}
	fields := parseKeyValues(raw.Body)
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registrar name"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "domain name commencement date"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "last updated date"))
	if statuses := statusesFromValues(valuesFor(fields, "domain status", "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromBlock(blockUntilNextField(raw.Body, "name servers information")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// TWWHOISParser reads the WHOIS answers for .tw, .xn--kprw13d and .xn--kpry57d.
type TWWHOISParser struct{}

var (
	twCreatedPattern = regexp.MustCompile(`(?im)^record created on\s+(.+?)(?:\s+\(|$)`)
	twExpiresPattern = regexp.MustCompile(`(?im)^record expires on\s+(.+?)(?:\s+\(|$)`)
)

// Name returns the parser's name.
func (TWWHOISParser) Name() string { return "whois-tld-tw" }

// Supports reports whether the parser reads raw.
func (TWWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".tw", ".xn--kprw13d", ".xn--kpry57d")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (TWWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (TWWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	bodyLower := strings.ToLower(raw.Body)
	if strings.Contains(bodyLower, "reserved name") {
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
		partial.Domain.Registered = false
		return partial, nil
	}
	fields := parseKeyValues(raw.Body)
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registration service provider"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, firstValue(fields, "registration service url"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstRegexpValue(twCreatedPattern, raw.Body))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstRegexpValue(twExpiresPattern, raw.Body))
	if statuses := statusesFromValues(valuesFor(fields, "domain status", "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromBlock(blockUntilNextField(raw.Body, "domain servers in listed order")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// SIWHOISParser reads the WHOIS answers for .si.
type SIWHOISParser struct{}

// Name returns the parser's name.
func (SIWHOISParser) Name() string { return "whois-tld-si" }

// Supports reports whether the parser reads raw.
func (SIWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".si")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (SIWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (SIWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "is forbidden") {
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
		partial.Domain.Registered = false
		return partial, nil
	}
	fields := parseKeyValues(raw.Body)
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registrar"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "created"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expire"))
	if statuses := statusesFromValues(valuesFor(fields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromValues(valuesFor(fields, "nserver", "name server", "nameserver")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	return partial, nil
}

// UAWHOISParser reads the WHOIS answers for .ua and .xn--j1amh.
type UAWHOISParser struct{}

// Name returns the parser's name.
func (UAWHOISParser) Name() string { return "whois-tld-ua" }

// Supports reports whether the parser reads raw.
func (UAWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".ua", ".xn--j1amh")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (UAWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (UAWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseKeyValues(raw.Body)
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "organization"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, firstValue(fields, "url"))
	statusSubject := beforeFirstText(raw.Body, "% registrar:")
	if statusSubject == "" {
		statusSubject = raw.Body
	}
	statusFields := parseKeyValues(statusSubject)
	if statuses := statusesFromValues(valuesFor(statusFields, "status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	return partial, nil
}

// IDWHOISParser reads the WHOIS answers for .id.
type IDWHOISParser struct{}

// Name returns the parser's name.
func (IDWHOISParser) Name() string { return "whois-tld-id" }

// Supports reports whether the parser reads raw.
func (IDWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".id")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (IDWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (IDWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseKeyValues(raw.Body)
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "sponsoring registrar organization"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "created on"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "last updated on"))
	return partial, nil
}

// KZWHOISParser reads the WHOIS answers for .kz and .xn--80ao21a.
type KZWHOISParser struct{}

// Name returns the parser's name.
func (KZWHOISParser) Name() string { return "whois-tld-kz" }

// Supports reports whether the parser reads raw.
func (KZWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".kz", ".xn--80ao21a")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (KZWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (KZWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "nothing found for this query") {
		markUnregistered(partial)
		return partial, nil
	}
	fields := parseFlexibleKeyValues(raw.Body)
	if domain := firstValue(fields, "domain name", "domain"); domain != "" {
		setDomainName(partial, domain)
	}
	partial.Registrant.Name = pickString(partial.Registrant.Name, firstValue(fields, "name", "person", "owner"))
	partial.Registrant.Organization = pickString(partial.Registrant.Organization, firstValue(fields, "organization", "organization name", "organization using domain name", "org"))
	partial.Registrant.Country = pickString(partial.Registrant.Country, firstValue(fields, "country", "registrant country"))
	partial.Registrant.Province = pickString(partial.Registrant.Province, firstValue(fields, "state", "state/province", "province"))
	partial.Registrant.City = pickString(partial.Registrant.City, firstValue(fields, "city"))
	partial.Registrant.Address = pickString(partial.Registrant.Address, firstValue(fields, "street address", "address"))
	partial.Registrant.PostalCode = pickString(partial.Registrant.PostalCode, firstValue(fields, "postal code", "zip code", "zip"))
	partial.Registrant.Email = pickString(partial.Registrant.Email, firstValue(fields, "e-mail", "email", "email address", "registrant email"))
	partial.Registrant.Phone = pickString(partial.Registrant.Phone, firstValue(fields, "phone", "phone number", "registrant phone"))
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "current registar", "current registrar", "registar", "registrar"))
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "domain created", "created"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "last modified", "modified"))
	if statuses := statusesFromValues(blockUntilNextField(raw.Body, "domain status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := kzNameservers(fields); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	partial.Registrant.Extra = appendRegistrationExtras(partial.Registrant.Extra, fields, []registrationExtraSpec{
		{Label: "IP Address", Keys: []string{"ip-address", "ip address", "ip", "server ip", "host ip"}},
		{Label: "Server", Keys: []string{"server", "server name", "server address"}},
		{Label: "Hosting Provider", Keys: []string{"hoster", "hosting", "hosting provider", "hosted by"}},
	})
	return partial, nil
}

var statusSeparatorPattern = regexp.MustCompile(`[,\n]`)

func hasQuerySuffix(raw model.RawResponse, suffixes ...string) bool {
	query := strings.ToLower(strings.TrimSpace(raw.Query))
	for _, suffix := range suffixes {
		if strings.HasSuffix(query, strings.ToLower(suffix)) {
			return true
		}
	}
	return false
}

func markUnregistered(partial *model.PartialResult) {
	partial.Status = model.StatusUnregistered
	partial.Domain.Registered = false
	partial.Domain.Reserved = false
}

func setDomainName(partial *model.PartialResult, value string) {
	domain := strings.ToLower(strings.TrimSuffix(firstToken(value), "."))
	if domain == "" {
		return
	}
	partial.Domain.Name = domain
	partial.Domain.PunycodeName = domain
}

func parseFlexibleKeyValues(body string) map[string][]string {
	fields := map[string][]string{}
	for _, line := range strings.Split(normalizeNewlines(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">>>") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimRight(strings.TrimSpace(key), ". \t")
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		fields[key] = append(fields[key], value)
	}
	return fields
}

func blockUntilNextField(body, heading string) []string {
	lines := strings.Split(normalizeNewlines(body), "\n")
	heading = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(heading), ":"))
	for i, line := range lines {
		key, value, ok := headingLine(line)
		if !ok || key != heading {
			continue
		}
		var out []string
		if value != "" {
			out = append(out, value)
		}
		for j := i + 1; j < len(lines); j++ {
			next := lines[j]
			trimmed := strings.TrimSpace(next)
			if trimmed == "" {
				if len(out) > 0 {
					break
				}
				continue
			}
			if len(out) > 0 && !strings.HasPrefix(next, " ") && !strings.HasPrefix(next, "\t") {
				if _, _, ok := headingLine(next); ok {
					break
				}
			}
			out = append(out, trimmed)
		}
		return out
	}
	return nil
}

func firstPlainLine(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, _, ok := headingLine(line); ok {
			continue
		}
		return line
	}
	return ""
}

func firstRegexpValue(pattern *regexp.Regexp, value string) string {
	matches := pattern.FindStringSubmatch(value)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func beforeFirstText(value, marker string) string {
	index := strings.Index(strings.ToLower(value), strings.ToLower(marker))
	if index <= 0 {
		return ""
	}
	return value[:index]
}

func statusesFromValues(values []string, source model.SourceName) []model.DomainStatus {
	var out []model.DomainStatus
	for _, value := range values {
		pieces := statusSeparatorPattern.Split(value, -1)
		for _, piece := range pieces {
			if strings.Contains(piece, "/") && !strings.Contains(piece, "://") {
				for _, slashPiece := range strings.Split(piece, "/") {
					out = appendStatus(out, slashPiece, source)
				}
				continue
			}
			out = appendStatus(out, piece, source)
		}
	}
	return out
}

func appendStatus(statuses []model.DomainStatus, value string, source model.SourceName) []model.DomainStatus {
	code := firstToken(value)
	if code == "" {
		return statuses
	}
	for _, status := range statuses {
		if status.Code == code {
			return statuses
		}
	}
	return append(statuses, model.DomainStatus{
		Code:   code,
		Label:  code,
		Source: string(source),
	})
}

func nameserversFromValues(values []string) []model.Nameserver {
	var out []model.Nameserver
	for _, value := range values {
		host := strings.ToLower(strings.TrimSuffix(firstToken(value), "."))
		if host == "" || !strings.Contains(host, ".") || isPlaceholderNameserver(host) {
			continue
		}
		if !hasNameserver(out, host) {
			out = append(out, model.Nameserver{Host: host})
		}
	}
	return out
}

// isPlaceholderNameserver matches slot fillers registries print for unused
// nameserver lines (.uz: "Name Server: not.defined."), which are not hosts.
func isPlaceholderNameserver(host string) bool {
	switch strings.Trim(strings.ToLower(host), ". ") {
	case "not.defined", "not-defined", "notdefined", "none", "n/a", "no.nameserver", "not.set", "undefined", "-":
		return true
	}
	return false
}

func kzNameservers(fields map[string][]string) []model.Nameserver {
	nameservers := nameserversFromValues(valuesFor(fields, "primary server", "secondary server", "name server", "nameserver", "nserver"))
	pairs := []struct {
		hostKeys []string
		ipKeys   []string
	}{
		{hostKeys: []string{"primary server"}, ipKeys: []string{"primary ip address", "primary ip-address", "primary ip"}},
		{hostKeys: []string{"secondary server"}, ipKeys: []string{"secondary ip address", "secondary ip-address", "secondary ip"}},
	}
	for _, pair := range pairs {
		hosts := valuesFor(fields, pair.hostKeys...)
		ips := valuesFor(fields, pair.ipKeys...)
		for index, hostValue := range hosts {
			host := strings.ToLower(strings.TrimSuffix(firstToken(hostValue), "."))
			if host == "" || !strings.Contains(host, ".") {
				continue
			}
			var ip string
			if index < len(ips) {
				ip = firstToken(ips[index])
			}
			nameservers = appendNameserverAddress(nameservers, host, ip)
		}
	}
	return nameservers
}

func appendNameserverAddress(values []model.Nameserver, host, address string) []model.Nameserver {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	address = strings.TrimSpace(address)
	if host == "" {
		return values
	}
	for i := range values {
		if values[i].Host != host {
			continue
		}
		if address != "" && !stringSliceContains(values[i].Addresses, address) {
			values[i].Addresses = append(values[i].Addresses, address)
		}
		return values
	}
	ns := model.Nameserver{Host: host}
	if address != "" {
		ns.Addresses = []string{address}
	}
	return append(values, ns)
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type registrationExtraSpec struct {
	Label string
	Keys  []string
}

func appendRegistrationExtras(current []model.RegistrationField, fields map[string][]string, specs []registrationExtraSpec) []model.RegistrationField {
	seen := map[string]bool{}
	for _, field := range current {
		seen[registrationExtraKey(field)] = true
	}
	out := current
	for _, spec := range specs {
		for _, value := range valuesFor(fields, spec.Keys...) {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			field := model.RegistrationField{
				Label:  spec.Label,
				Value:  value,
				Source: string(model.SourceWHOIS),
			}
			key := registrationExtraKey(field)
			if seen[key] {
				continue
			}
			out = append(out, field)
			seen[key] = true
		}
	}
	return out
}

func registrationExtraKey(field model.RegistrationField) string {
	return strings.ToLower(strings.TrimSpace(field.Label)) + "\x00" +
		strings.ToLower(strings.TrimSpace(field.Value)) + "\x00" +
		strings.ToLower(strings.TrimSpace(field.Source))
}

func applyDNSSECText(partial *model.PartialResult, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	partial.DNSSEC.Text = value
	signed := dnssecTextMeansSigned(value)
	partial.DNSSEC.Signed = &signed
}

func dnssecTextMeansSigned(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value != "" &&
		value != "0" &&
		value != "no" &&
		value != "false" &&
		!strings.Contains(value, "unsigned") &&
		!strings.Contains(value, "not signed")
}

func isNLUnregistered(body string) bool {
	body = strings.ToLower(body)
	return strings.Contains(body, ".nl is free") || strings.Contains(body, "domain name is free")
}

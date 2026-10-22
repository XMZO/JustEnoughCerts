package parse

import (
	"context"
	"strings"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// ITWHOISParser reads the WHOIS answers for .it.
type ITWHOISParser struct{}

// Name returns the parser's name.
func (ITWHOISParser) Name() string { return "whois-tld-it" }

// Supports reports whether the parser reads raw.
func (ITWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && strings.HasSuffix(strings.ToLower(raw.Query), ".it")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (ITWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (ITWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseKeyValues(raw.Body)
	statusValue := firstValue(fields, "status")
	switch strings.ToLower(strings.TrimSpace(statusValue)) {
	case "available":
		partial.Status = model.StatusUnregistered
		partial.Domain.Registered = false
		return partial, nil
	case "unassignable":
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
		partial.Domain.Registered = false
		return partial, nil
	}

	if statusValue != "" {
		partial.Statuses = statusesFromSlashList(statusValue, raw.Source)
	}
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "created"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "last update"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expire date"))

	registrarFields := parseKeyValues(strings.Join(blockLinesFlexible(raw.Body, "registrar"), "\n"))
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(registrarFields, "organization", "name"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, firstValue(registrarFields, "web", "website"))
	registrantFields := parseKeyValues(strings.Join(blockLinesFlexible(raw.Body, "registrant"), "\n"))
	partial.Registrant.Country = pickString(partial.Registrant.Country, firstValue(registrantFields, "country"))

	if nameservers := nameserversFromBlock(blockLinesFlexible(raw.Body, "nameservers")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	if signedText := firstValue(fields, "signed"); signedText != "" {
		partial.DNSSEC.Text = signedText
		signed := isSignedDNSSECText(signedText)
		partial.DNSSEC.Signed = &signed
	}

	return partial, nil
}

// EUWHOISParser reads the WHOIS answers for .eu and those that name EURid.
type EUWHOISParser struct{}

// Name returns the parser's name.
func (EUWHOISParser) Name() string { return "whois-tld-eu" }

// Supports reports whether the parser reads raw.
func (EUWHOISParser) Supports(raw model.RawResponse) bool {
	if raw.Source != model.SourceWHOIS {
		return false
	}
	body := strings.ToLower(raw.Body)
	return strings.HasSuffix(strings.ToLower(raw.Query), ".eu") || strings.Contains(body, "eurid")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (EUWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (EUWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil || partial.Status != model.StatusRegistered {
		return partial, err
	}

	registrarFields := parseKeyValues(strings.Join(blockLinesFlexible(raw.Body, "registrar"), "\n"))
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(registrarFields, "name"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, firstValue(registrarFields, "website"))

	if nameservers := nameserversFromBlock(blockLinesFlexible(raw.Body, "name servers")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	if keys := blockLinesFlexible(raw.Body, "keys"); len(keys) > 0 {
		partial.DNSSEC.Text = strings.Join(keys, "; ")
		signed := true
		partial.DNSSEC.Signed = &signed
	}

	return partial, nil
}

// BEWHOISParser reads the WHOIS answers for .be.
type BEWHOISParser struct{}

// Name returns the parser's name.
func (BEWHOISParser) Name() string { return "whois-tld-be" }

// Supports reports whether the parser reads raw.
func (BEWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && strings.HasSuffix(strings.ToLower(raw.Query), ".be")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (BEWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (BEWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseKeyValues(raw.Body)
	switch strings.ToLower(firstValue(fields, "status")) {
	case "available":
		partial.Status = model.StatusUnregistered
		partial.Domain.Registered = false
		return partial, nil
	case "not allowed":
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
		partial.Domain.Registered = false
		return partial, nil
	}

	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "registered"))
	registrarFields := parseKeyValues(strings.Join(blockLinesFlexible(raw.Body, "registrar"), "\n"))
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(registrarFields, "name"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, firstValue(registrarFields, "website"))

	if nameservers := nameserversFromBlock(blockLinesFlexible(raw.Body, "nameservers")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	if flags := statusLinesFromBlock(blockLinesFlexible(raw.Body, "flags"), raw.Source); len(flags) > 0 {
		partial.Statuses = flags
	}
	if keys := blockLinesFlexible(raw.Body, "keys"); len(keys) > 0 {
		partial.DNSSEC.Text = strings.Join(keys, "; ")
		signed := true
		partial.DNSSEC.Signed = &signed
	}

	return partial, nil
}

func blockLinesFlexible(body, heading string) []string {
	lines := strings.Split(normalizeNewlines(body), "\n")
	heading = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(heading), ":"))
	for i, line := range lines {
		key, value, ok := flexibleHeadingLine(line)
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
			if len(out) > 0 && isTopLevelBlockHeading(next) {
				break
			}
			out = append(out, trimmed)
		}
		return out
	}
	return nil
}

func flexibleHeadingLine(line string) (string, string, bool) {
	if key, value, ok := headingLine(line); ok {
		return key, value, true
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
		return "", "", false
	}
	key := strings.ToLower(strings.TrimSuffix(trimmed, ":"))
	return key, "", true
}

func isTopLevelBlockHeading(line string) bool {
	if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
		return false
	}
	key, _, ok := flexibleHeadingLine(line)
	if !ok {
		return false
	}
	switch key {
	case "domain", "registrant", "admin contact", "technical contacts", "technical contact", "registrar", "nameservers", "name servers", "keys", "flags":
		return true
	default:
		return false
	}
}

func nameserversFromBlock(lines []string) []model.Nameserver {
	var out []model.Nameserver
	for _, line := range lines {
		host := strings.ToLower(strings.TrimSuffix(firstToken(line), "."))
		if host == "" || !strings.Contains(host, ".") || strings.Contains(host, ":") {
			continue
		}
		if !hasNameserver(out, host) {
			out = append(out, model.Nameserver{Host: host})
		}
	}
	return out
}

func statusLinesFromBlock(lines []string, source model.SourceName) []model.DomainStatus {
	var statuses []model.DomainStatus
	for _, line := range lines {
		code := strings.TrimSpace(firstToken(line))
		if code == "" {
			continue
		}
		statuses = append(statuses, model.DomainStatus{
			Code:   code,
			Label:  code,
			Source: string(source),
		})
	}
	return statuses
}

func statusesFromSlashList(value string, source model.SourceName) []model.DomainStatus {
	var statuses []model.DomainStatus
	for _, item := range strings.Split(value, "/") {
		code := strings.TrimSpace(item)
		if code == "" {
			continue
		}
		statuses = append(statuses, model.DomainStatus{
			Code:   code,
			Label:  code,
			Source: string(source),
		})
	}
	return statuses
}

func pickString(current, incoming string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return strings.TrimSpace(incoming)
}

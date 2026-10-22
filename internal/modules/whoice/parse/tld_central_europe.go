package parse

import (
	"context"
	"regexp"
	"strings"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// PLWHOISParser reads the WHOIS answers for .pl.
type PLWHOISParser struct{}

// Name returns the parser's name.
func (PLWHOISParser) Name() string { return "whois-tld-pl" }

// Supports reports whether the parser reads raw.
func (PLWHOISParser) Supports(raw model.RawResponse) bool {
	if raw.Source != model.SourceWHOIS {
		return false
	}
	return strings.HasSuffix(strings.ToLower(raw.Query), ".pl")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (PLWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (PLWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if isPLUnregistered(raw.Body) {
		partial.Status = model.StatusUnregistered
		partial.Domain.Registered = false
		return partial, nil
	}

	fields := parseKeyValues(raw.Body)
	if domain := firstValue(fields, "domain name"); domain != "" {
		partial.Domain.Name = strings.ToLower(firstToken(domain))
		partial.Domain.PunycodeName = partial.Domain.Name
	}
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "created"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "last modified"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "renewal date", "expiration date"))

	registrarName, registrarURL := plRegistrar(raw.Body)
	partial.Registrar.Name = pickString(partial.Registrar.Name, registrarName)
	partial.Registrar.URL = pickString(partial.Registrar.URL, registrarURL)

	if nameservers := nameserversFromBlock(blockLinesFlexible(raw.Body, "nameservers")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	if dnssec := firstValue(fields, "dnssec"); dnssec != "" {
		partial.DNSSEC.Text = dnssec
		signed := isSignedDNSSECText(dnssec)
		partial.DNSSEC.Signed = &signed
	}

	return partial, nil
}

// CZWHOISParser reads the WHOIS answers for .cz.
type CZWHOISParser struct{}

// Name returns the parser's name.
func (CZWHOISParser) Name() string { return "whois-tld-cz" }

// Supports reports whether the parser reads raw.
func (CZWHOISParser) Supports(raw model.RawResponse) bool {
	if raw.Source != model.SourceWHOIS {
		return false
	}
	return strings.HasSuffix(strings.ToLower(raw.Query), ".cz")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (CZWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (CZWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "no entries found") {
		partial.Status = model.StatusUnregistered
		partial.Domain.Registered = false
		return partial, nil
	}

	head := beforeFirstContact(raw.Body)
	if head == "" {
		head = raw.Body
	}
	fields := parseKeyValues(head)
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "registered"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expire"))
	partial.Dates.UpdatedAt = pickString("", firstValue(fields, "changed"))
	partial.Registrar.Name = pickString(partial.Registrar.Name, firstValue(fields, "registrar"))
	if statuses := valuesFor(fields, "status"); len(statuses) > 0 {
		partial.Statuses = nil
		for _, status := range statuses {
			status = strings.TrimSpace(status)
			if status == "" {
				continue
			}
			partial.Statuses = append(partial.Statuses, model.DomainStatus{
				Code:   status,
				Label:  status,
				Source: string(raw.Source),
			})
		}
	}

	return partial, nil
}

// HUWHOISParser reads the WHOIS answers for .hu.
type HUWHOISParser struct{}

// Name returns the parser's name.
func (HUWHOISParser) Name() string { return "whois-tld-hu" }

// Supports reports whether the parser reads raw.
func (HUWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && strings.HasSuffix(strings.ToLower(raw.Query), ".hu")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (HUWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (HUWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "not registered") {
		partial.Status = model.StatusUnregistered
		partial.Domain.Registered = false
		return partial, nil
	}

	fields := parseKeyValues(raw.Body)
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "registered", "record created"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expiry date"))
	if nameservers := nameserversFromWhitespaceList(firstValue(fields, "name servers")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}

	return partial, nil
}

// SKWHOISParser reads the WHOIS answers for .sk.
type SKWHOISParser struct{}

// Name returns the parser's name.
func (SKWHOISParser) Name() string { return "whois-tld-sk" }

// Supports reports whether the parser reads raw.
func (SKWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && strings.HasSuffix(strings.ToLower(raw.Query), ".sk")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (SKWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (SKWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "not found") {
		partial.Status = model.StatusUnregistered
		partial.Domain.Registered = false
		return partial, nil
	}

	fields := parseKeyValues(raw.Body)
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "created"))
	partial.Dates.UpdatedAt = pickString(partial.Dates.UpdatedAt, firstValue(fields, "updated"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "valid until"))
	if statuses := splitStatusList(firstValue(fields, "status"), ","); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if registrar := skRegistrar(raw.Body); registrar != "" {
		partial.Registrar.Name = registrar
	}

	return partial, nil
}

// ROWHOISParser reads the WHOIS answers for .ro.
type ROWHOISParser struct{}

// Name returns the parser's name.
func (ROWHOISParser) Name() string { return "whois-tld-ro" }

// Supports reports whether the parser reads raw.
func (ROWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && strings.HasSuffix(strings.ToLower(raw.Query), ".ro")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (ROWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (ROWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	if strings.Contains(strings.ToLower(raw.Body), "no entries found") {
		partial.Status = model.StatusUnregistered
		partial.Domain.Registered = false
		return partial, nil
	}

	fields := parseKeyValues(raw.Body)
	partial.Dates.CreatedAt = pickString(partial.Dates.CreatedAt, firstValue(fields, "registered on"))
	partial.Dates.ExpiresAt = pickString(partial.Dates.ExpiresAt, firstValue(fields, "expires on"))
	partial.Registrar.URL = pickString(partial.Registrar.URL, firstValue(fields, "referral url"))

	return partial, nil
}

func isPLUnregistered(body string) bool {
	body = strings.ToLower(body)
	return strings.Contains(body, "no information available about domain name") ||
		strings.Contains(body, "no information available")
}

func nameserversFromWhitespaceList(value string) []model.Nameserver {
	var out []model.Nameserver
	for _, item := range strings.Fields(value) {
		host := strings.ToLower(strings.TrimSuffix(item, "."))
		if host == "" || !strings.Contains(host, ".") {
			continue
		}
		if !hasNameserver(out, host) {
			out = append(out, model.Nameserver{Host: host})
		}
	}
	return out
}

func splitStatusList(value, separator string) []model.DomainStatus {
	var out []model.DomainStatus
	for _, item := range strings.Split(value, separator) {
		code := strings.TrimSpace(item)
		if code == "" {
			continue
		}
		out = append(out, model.DomainStatus{
			Code:   code,
			Label:  code,
			Source: string(model.SourceWHOIS),
		})
	}
	return out
}

func skRegistrar(body string) string {
	lines := strings.Split(normalizeNewlines(body), "\n")
	for i, line := range lines {
		key, _, ok := headingLine(line)
		if !ok || key != "registrar" {
			continue
		}
		block := strings.Join(lines[i:smallestInt(i+8, len(lines))], "\n")
		fields := parseKeyValues(block)
		return firstValue(fields, "organization", "name")
	}
	return ""
}

func smallestInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func plRegistrar(body string) (string, string) {
	lines := strings.Split(normalizeNewlines(body), "\n")
	for i, line := range lines {
		key, value, ok := flexibleHeadingLine(line)
		if !ok || key != "registrar" {
			continue
		}
		var block []string
		if value != "" {
			block = append(block, value)
		}
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == "" {
				if len(block) > 0 {
					break
				}
				continue
			}
			if strings.HasPrefix(strings.ToLower(trimmed), "whois database responses") {
				break
			}
			if len(block) > 0 && looksLikeTopLevelField(trimmed) && !looksLikeURLLine(trimmed) {
				break
			}
			block = append(block, trimmed)
		}
		return registrarNameAndURL(block)
	}
	return "", ""
}

func registrarNameAndURL(lines []string) (string, string) {
	var name string
	var url string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if extracted := firstURL(line); extracted != "" {
			url = extracted
			continue
		}
		if name == "" {
			name = line
		}
	}
	return name, url
}

func firstURL(value string) string {
	fields := strings.Fields(value)
	for _, field := range fields {
		field = strings.Trim(field, ".,;()[]<>")
		lower := strings.ToLower(field)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			return field
		}
		if strings.HasPrefix(lower, "www.") {
			return "https://" + field
		}
	}
	return ""
}

func looksLikeURLLine(value string) bool {
	return firstURL(value) != ""
}

func looksLikeTopLevelField(value string) bool {
	key, _, ok := headingLine(value)
	return ok && key != ""
}

var contactBoundaryPattern = regexp.MustCompile(`(?im)^contact\s*:`)

func beforeFirstContact(body string) string {
	index := contactBoundaryPattern.FindStringIndex(body)
	if index == nil || index[0] <= 0 {
		return ""
	}
	return body[:index[0]]
}

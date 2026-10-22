package parse

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// UKWHOISParser reads the WHOIS answers for .uk and those that look like the .uk registry's.
type UKWHOISParser struct{}

// Name returns the parser's name.
func (UKWHOISParser) Name() string { return "whois-tld-uk" }

// Supports reports whether the parser reads raw.
func (UKWHOISParser) Supports(raw model.RawResponse) bool {
	if raw.Source != model.SourceWHOIS {
		return false
	}
	query := strings.ToLower(raw.Query)
	body := strings.ToLower(raw.Body)
	return strings.HasSuffix(query, ".uk") ||
		strings.Contains(body, "nominet") || //nolint:misspell // Nominet is the .uk registry, named in its answers
		(strings.Contains(body, "registration status:") && strings.Contains(body, "name servers:"))
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (UKWHOISParser) Priority() int { return 90 }

// Parse reads the answer.
func (UKWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil || partial.Status != model.StatusRegistered {
		return partial, err
	}

	if domain := firstToken(blockValue(raw.Body, "domain name")); domain != "" {
		partial.Domain.Name = strings.ToLower(domain)
		partial.Domain.PunycodeName = partial.Domain.Name
	}

	registrarLines := blockLines(raw.Body, "registrar")
	for _, line := range registrarLines {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "url") {
			partial.Registrar.URL = strings.TrimSpace(value)
			continue
		}
		if partial.Registrar.Name == "" {
			partial.Registrar.Name = cleanUKRegistrar(line)
		}
	}

	statusLines := blockLines(raw.Body, "registration status")
	if len(statusLines) > 0 {
		partial.Statuses = nil
		for _, line := range statusLines {
			status := strings.Trim(strings.TrimSpace(line), ".")
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

	nameServerLines := blockLines(raw.Body, "name servers")
	if len(nameServerLines) > 0 {
		partial.Nameservers = nil
		for _, line := range nameServerLines {
			if strings.EqualFold(strings.TrimSpace(line), "no name servers listed.") {
				continue
			}
			host := strings.ToLower(strings.TrimSuffix(firstToken(line), "."))
			if host != "" && !hasNameserver(partial.Nameservers, host) {
				partial.Nameservers = append(partial.Nameservers, model.Nameserver{Host: host})
			}
		}
	}

	if dnssec := blockValue(raw.Body, "dnssec"); dnssec != "" {
		partial.DNSSEC.Text = dnssec
		signed := isSignedDNSSECText(dnssec)
		partial.DNSSEC.Signed = &signed
	}

	return partial, nil
}

// JPWHOISParser reads the WHOIS answers for .jp and those in JPRS's bracketed form.
type JPWHOISParser struct{}

// Name returns the parser's name.
func (JPWHOISParser) Name() string { return "whois-tld-jp" }

// Supports reports whether the parser reads raw.
func (JPWHOISParser) Supports(raw model.RawResponse) bool {
	if raw.Source != model.SourceWHOIS {
		return false
	}
	query := strings.ToLower(raw.Query)
	return strings.HasSuffix(query, ".jp") || strings.Contains(strings.ToLower(raw.Body), "[domain name]")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (JPWHOISParser) Priority() int { return 90 }

// Parse reads the answer.
func (JPWHOISParser) Parse(_ context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial := &model.PartialResult{
		Source: raw.Source,
		Status: model.StatusRegistered,
		Raw:    model.RawData{WHOIS: raw.Body},
		Domain: model.DomainInfo{
			Name:             q.Query,
			PunycodeName:     q.Query,
			UnicodeName:      q.UnicodeQuery,
			Suffix:           q.Suffix,
			RegisteredDomain: q.RegisteredDomain,
			Registered:       q.Type == model.QueryDomain,
		},
	}

	if raw.Body == "" {
		partial.Status = model.StatusUnknown
		partial.Warnings = append(partial.Warnings, "empty WHOIS response")
		return partial, nil
	}
	if matchesAny(reservedPatterns, raw.Body) {
		partial.Status = model.StatusReserved
		partial.Domain.Reserved = true
	}
	if matchesAny(unregisteredPatterns, raw.Body) {
		partial.Status = model.StatusUnregistered
		partial.Domain.Registered = false
		return partial, nil
	}

	fields := parseBracketFields(raw.Body)
	if domain := firstBracketValue(fields, "domain name"); domain != "" {
		partial.Domain.Name = strings.ToLower(domain)
		partial.Domain.PunycodeName = partial.Domain.Name
	}
	// General-use names (example.jp) and organizational ones (example.co.jp)
	// label the same data differently.
	if registrant := firstBracketValue(fields, "registrant", "organization"); registrant != "" {
		partial.Registrant.Organization = registrant
	}
	partial.Dates.CreatedAt = normalizeJPDate(firstBracketValue(fields, "created on", "registered date", "connected date"))
	partial.Dates.ExpiresAt = normalizeJPDate(firstBracketValue(fields, "expires on"))
	partial.Dates.UpdatedAt = normalizeJPDate(firstBracketValue(fields, "last updated", "last update"))

	for _, status := range fields["status"] {
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
	for _, value := range fields["name server"] {
		host := strings.ToLower(strings.TrimSuffix(firstToken(value), "."))
		if host != "" && !hasNameserver(partial.Nameservers, host) {
			partial.Nameservers = append(partial.Nameservers, model.Nameserver{Host: host})
		}
	}
	if signingKeys := nonEmptyValues(fields["signing key"]); len(signingKeys) > 0 {
		partial.DNSSEC.Text = strings.Join(signingKeys, "; ")
		signed := true
		partial.DNSSEC.Signed = &signed
	}

	return partial, nil
}

var ukRegistrarTagPattern = regexp.MustCompile(`\s+\[[^\]]+\]\s*$`)

func cleanUKRegistrar(value string) string {
	value = strings.TrimSpace(value)
	return strings.TrimSpace(ukRegistrarTagPattern.ReplaceAllString(value, ""))
}

func blockValue(body, heading string) string {
	lines := blockLines(body, heading)
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

func blockLines(body, heading string) []string {
	lines := strings.Split(normalizeNewlines(body), "\n")
	heading = strings.ToLower(strings.TrimSpace(heading))
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

func headingLine(line string) (string, string, bool) {
	key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
	if !ok {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

var bracketFieldPattern = regexp.MustCompile(`^\[([^\]]+)\]\s*(.*)$`)

func parseBracketFields(body string) map[string][]string {
	fields := map[string][]string{}
	for _, line := range strings.Split(normalizeNewlines(body), "\n") {
		matches := bracketFieldPattern.FindStringSubmatch(strings.TrimSpace(line))
		if matches == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(matches[1]))
		value := strings.TrimSpace(matches[2])
		fields[key] = append(fields[key], value)
	}
	return fields
}

func firstBracketValue(fields map[string][]string, keys ...string) string {
	for _, key := range keys {
		if value := firstBracketValueFor(fields, key); value != "" {
			return value
		}
	}
	return ""
}

func firstBracketValueFor(fields map[string][]string, key string) string {
	for _, value := range fields[strings.ToLower(key)] {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstToken(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

var jstZone = time.FixedZone("JST", 9*3600)

// normalizeJPDate turns "2026/04/01 01:02:00 (JST)" into RFC 3339 with the
// +09:00 offset; dates without a time become "2026-04-01".
func normalizeJPDate(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "(JST)") {
		if parsed, err := time.ParseInLocation("2006/01/02 15:04:05", strings.TrimSpace(strings.TrimSuffix(value, "(JST)")), jstZone); err == nil {
			return parsed.Format(time.RFC3339)
		}
	}
	value = strings.TrimSuffix(value, " (JST)")
	return strings.ReplaceAll(value, "/", "-")
}

func normalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func nonEmptyValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func isSignedDNSSECText(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value != "" && !strings.Contains(value, "unsigned") && !strings.Contains(value, "not signed")
}

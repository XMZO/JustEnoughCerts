package parse

import (
	"context"
	"regexp"
	"strings"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// FRWHOISParser reads the WHOIS answers for .fr and those with AFNIC's eppstatus lines.
type FRWHOISParser struct{}

// Name returns the parser's name.
func (FRWHOISParser) Name() string { return "whois-tld-fr" }

// Supports reports whether the parser reads raw.
func (FRWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && (strings.HasSuffix(strings.ToLower(raw.Query), ".fr") || strings.Contains(strings.ToLower(raw.Body), "eppstatus:"))
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (FRWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (FRWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil || partial.Status != model.StatusRegistered {
		return partial, err
	}
	fields := parseKeyValues(raw.Body)
	if url := firstValue(fields, "website"); url != "" {
		partial.Registrar.URL = url
	}
	partial.Dates.UpdatedAt = firstValue(fields, "last-update")
	statusSubject := raw.Body
	if beforeSource := beforeFirstSource(raw.Body); beforeSource != "" {
		statusSubject = beforeSource
	}
	statusFields := parseKeyValues(statusSubject)
	if values := valuesFor(statusFields, "eppstatus"); len(values) > 0 {
		partial.Statuses = nil
		for _, value := range values {
			code := strings.Fields(value)
			if len(code) == 0 {
				continue
			}
			partial.Statuses = append(partial.Statuses, model.DomainStatus{
				Code:   code[0],
				Label:  code[0],
				Source: string(raw.Source),
			})
		}
	}
	return partial, nil
}

// CNWHOISParser reads the WHOIS answers for .cn.
type CNWHOISParser struct{}

// Name returns the parser's name.
func (CNWHOISParser) Name() string { return "whois-tld-cn" }

// Supports reports whether the parser reads raw.
func (CNWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && strings.HasSuffix(strings.ToLower(raw.Query), ".cn")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (CNWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (CNWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseKeyValues(raw.Body)
	partial.Dates.CreatedAt = firstValue(fields, "registration time")
	partial.Dates.ExpiresAt = firstValue(fields, "expiration time")
	partial.Registrant.Email = pickString(partial.Registrant.Email, firstValue(fields, "registrant contact email", "registrant email", "email"))
	partial.Dates.CreatedAt = normalizeCNDate(partial.Dates.CreatedAt)
	partial.Dates.ExpiresAt = normalizeCNDate(partial.Dates.ExpiresAt)
	partial.Dates.UpdatedAt = normalizeCNDate(partial.Dates.UpdatedAt)
	return partial, nil
}

// BRWHOISParser reads the WHOIS answers for .br.
type BRWHOISParser struct{}

// Name returns the parser's name.
func (BRWHOISParser) Name() string { return "whois-tld-br" }

// Supports reports whether the parser reads raw.
func (BRWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && strings.HasSuffix(strings.ToLower(raw.Query), ".br")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (BRWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (BRWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	fields := parseKeyValues(raw.Body)
	partial.Dates.CreatedAt = firstDateToken(partial.Dates.CreatedAt)
	partial.Registrant.Country = firstValue(fields, "country")
	return partial, nil
}

var sourceBoundaryPattern = regexp.MustCompile(`(?im)^source\s*:`)

func beforeFirstSource(body string) string {
	index := sourceBoundaryPattern.FindStringIndex(body)
	if index == nil || index[0] <= 0 {
		return ""
	}
	return body[:index[0]]
}

func normalizeCNDate(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "Z") || strings.Contains(value, "T") {
		return value
	}
	if strings.Contains(value, " ") && len(value) >= 19 {
		return strings.Replace(value[:19], " ", "T", 1) + "+08:00"
	}
	return value
}

func firstDateToken(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return value
	}
	return fields[0]
}

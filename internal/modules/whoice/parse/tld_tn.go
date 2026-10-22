package parse

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// TNWHOISParser reads whois.ati.tn: keys are padded with dots
// ("Creation date.......: ...") and contacts and nameservers sit under
// plain heading lines ("Owner Contact", "DNS servers") with repeated keys.
type TNWHOISParser struct{}

// Name returns the parser's name.
func (TNWHOISParser) Name() string { return "whois-tld-tn" }

// Supports reports whether the parser reads raw.
func (TNWHOISParser) Supports(raw model.RawResponse) bool {
	return raw.Source == model.SourceWHOIS && hasQuerySuffix(raw, ".tn", ".xn--pgbs0dh")
}

// Priority ranks the parser: of those that support an answer, the highest reads it.
func (TNWHOISParser) Priority() int { return 88 }

// Parse reads the answer.
func (TNWHOISParser) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	partial, err := WHOISParser{}.Parse(ctx, raw, q)
	if err != nil {
		return partial, err
	}
	sections := tnSections(raw.Body)
	main := sections[""]
	if domain := firstValue(main, "domain name"); domain != "" {
		setDomainName(partial, domain)
		if partial.Status == model.StatusUnknown {
			partial.Status = model.StatusRegistered
		}
	}
	partial.Registrar.Name = pickString(firstValue(main, "registrar"), partial.Registrar.Name)
	partial.Dates.CreatedAt = pickString(tnDate(firstValue(main, "creation date")), partial.Dates.CreatedAt)
	// "dnssec" trails the last contact block rather than the details.
	for _, section := range sections {
		if value := firstValue(section, "dnssec"); value != "" {
			partial.DNSSEC.Text = value
			break
		}
	}
	if statuses := statusesFromValues(valuesFor(main, "domain status"), raw.Source); len(statuses) > 0 {
		partial.Statuses = statuses
	}
	if nameservers := nameserversFromValues(valuesFor(sections["dns servers"], "name")); len(nameservers) > 0 {
		partial.Nameservers = nameservers
	}
	owner := sections["owner contact"]
	partial.Registrant.Name = strings.TrimSpace(strings.Join([]string{firstValue(owner, "first name"), firstValue(owner, "name")}, " "))
	partial.Registrant.Address = strings.TrimSpace(strings.Join([]string{firstValue(owner, "address"), firstValue(owner, "address2")}, " "))
	partial.Registrant.City = firstValue(owner, "city")
	partial.Registrant.Province = firstValue(owner, "stateprovince")
	partial.Registrant.PostalCode = firstValue(owner, "zip code")
	partial.Registrant.Country = firstValue(owner, "country")
	partial.Registrant.Phone = firstValue(owner, "phone")
	partial.Registrant.Email = contactEmail(firstValue(owner, "email"))
	return partial, nil
}

// tnSections groups key/value lines by the heading line above them; lines
// before any heading (and under "Details:") belong to "".
func tnSections(body string) map[string]map[string][]string {
	sections := map[string]map[string][]string{"": {}}
	current := ""
	for _, line := range strings.Split(normalizeNewlines(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if heading, ok := tnHeadings[strings.ToLower(strings.TrimSuffix(line, ":"))]; ok {
			current = heading
			if sections[current] == nil {
				sections[current] = map[string][]string{}
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimRight(strings.TrimSpace(key), ". \t"))
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		sections[current][key] = append(sections[current][key], value)
	}
	return sections
}

var tnHeadings = map[string]string{
	"details":                "",
	"owner contact":          "owner contact",
	"administrativ contact":  "admin contact",
	"administrative contact": "admin contact",
	"technical contact":      "technical contact",
	"dns servers":            "dns servers",
}

var tnDatePattern = regexp.MustCompile(`^(\d{2}-\d{2}-\d{4} \d{2}:\d{2}:\d{2}) GMT([+-]\d{1,2})$`)

// tnDate turns "17-06-2026 18:55:01 GMT+1" into RFC 3339.
func tnDate(value string) string {
	value = strings.TrimSpace(value)
	match := tnDatePattern.FindStringSubmatch(value)
	if match == nil {
		return value
	}
	hours, err := strconv.Atoi(match[2])
	if err != nil {
		return value
	}
	parsed, err := time.ParseInLocation("02-01-2006 15:04:05", match[1], time.FixedZone("", hours*3600))
	if err != nil {
		return value
	}
	return parsed.Format(time.RFC3339)
}

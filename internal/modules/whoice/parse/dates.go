package parse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Time reads a date a parser left in DateInfo, as the registry wrote it.
// It reads what Whoice's merger reads (parseTime after normalizeDates) and
// the other forms the parsers leave (registryLayouts, registryForm).
// Placeholders such as "N/A" and values in no known form read as nothing.
// A value without a time zone is read as UTC.
func Time(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" || isDatePlaceholder(value) {
		return time.Time{}, false
	}
	if parsed, ok := readDate(value); ok {
		return parsed, true
	}
	if rewritten := registryForm(value); rewritten != value {
		return readDate(rewritten)
	}
	return time.Time{}, false
}

func readDate(value string) (time.Time, bool) {
	if parsed, ok := parseTime(value); ok {
		return parsed, true
	}
	for _, layouts := range [][]string{dayMonthNameLayouts, registryLayouts} {
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed, true
			}
		}
	}
	return time.Time{}, false
}

// The functions and layouts from here to registryLayouts are Whoice's
// (services/lookup-api/internal/merger/merger.go), as they are there.

func parseTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	if parsed, ok := parseZonedTime(value); ok {
		return parsed, true
	}
	layouts := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2006.01.02 15:04:05",
		"02-Jan-2006 15:04:05",
		"02-Jan-2006",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// zonedLayouts carry an explicit UTC offset or a UTC/GMT suffix, so they can
// be rewritten as RFC 3339 without guessing a time zone.
var zonedLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02T15:04:05 MST",
	"2006-01-02 15:04:05 MST",
	"02-Jan-2006 15:04:05 MST",
	"2006.01.02 15:04:05 MST",
	"Mon Jan 2 15:04:05 MST 2006",
	"Mon Jan _2 15:04:05 MST 2006",
}

func parseZonedTime(value string) (time.Time, bool) {
	for _, layout := range zonedLayouts {
		parsed, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		if strings.Contains(layout, "MST") {
			// Go accepts any abbreviation for MST and assumes offset 0 for
			// unknown ones; trust only UTC and GMT.
			if name, _ := parsed.Zone(); name != "UTC" && name != "GMT" {
				continue
			}
		}
		return parsed, true
	}
	return time.Time{}, false
}

// dayMonthNameLayouts are date-only forms with a month name, such as
// "09-Apr-2026" or "16 July 1999" (nic.pa); they are rewritten as 2006-01-02.
var dayMonthNameLayouts = []string{"02-Jan-2006", "2 January 2006", "2 Jan 2006"}

func isDatePlaceholder(value string) bool {
	switch strings.ToLower(strings.Trim(value, " .")) {
	case "n/a", "na", "-", "--", "none", "null", "unknown", "not available", "not defined":
		return true
	}
	return false
}

// registryLayouts are not Whoice's: they are the other forms the parsers
// leave as the registries wrote them (see testdata/whois), which Whoice
// shows as text and JEC reads. The registries that put the day first
// (.bd, .cz, .fi, .hk, .pt) write it first.
var registryLayouts = []string{
	"20060102",                // .br, .il
	"20060102 15:04:05",       // .at
	"2.1.2006",                // .cz, .fi
	"2.1.2006 15:04:05",       // .cz, .fi
	"02/01/2006",              // .bd
	"02/01/2006 15:04:05",     // .pt
	"02-01-2006",              // .hk
	"2006. 01. 02.",           // .kr
	"2006-Jan-02.",            // .tr
	"Mon Jan _2 2006",         // .be
	time.ANSIC,                // .kg
	"2 January 2006 15:04:05", // .gg, .je, after registryForm
}

var (
	// ordinalDate is Island Networks' "7th June 2005 at 08:09:10.000000"
	// (.gg, .je).
	ordinalDate = regexp.MustCompile(`^(\d{1,2})(?:st|nd|rd|th) (.+?) at (.+)$`)
	// gmtOffset is .kz's "2018-03-22 11:55:21 (GMT+0:00)".
	gmtOffset = regexp.MustCompile(`^(.*\S)\s*\(GMT([+-])(\d{1,2}):(\d{2})\)$`)
)

// registryForm rewrites the forms no layout reads into ones that one does,
// and returns other values as they are.
func registryForm(value string) string {
	if match := ordinalDate.FindStringSubmatch(value); match != nil {
		return match[1] + " " + match[2] + " " + match[3]
	}
	if match := gmtOffset.FindStringSubmatch(value); match != nil {
		hours, err := strconv.Atoi(match[3])
		if err != nil {
			return value
		}
		return fmt.Sprintf("%s%s%02d:%s", match[1], match[2], hours, match[4])
	}
	return value
}

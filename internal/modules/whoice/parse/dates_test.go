package parse

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

func TestTime(t *testing.T) {
	utc := func(year int, month time.Month, day, hour, minute, second int) time.Time {
		return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
	}
	tests := []struct {
		name  string
		value string
		want  time.Time
	}{
		{"jp date", "2027-03-31", utc(2027, 3, 31, 0, 0, 0)},
		{"jp time", "2026-04-01T01:05:04+09:00", utc(2026, 3, 31, 16, 5, 4)},
		{"cn", "2027-03-17T12:00:00+08:00", utc(2027, 3, 17, 4, 0, 0)},
		{"tw", "2027-01-10 12:00:00", utc(2027, 1, 10, 12, 0, 0)},
		{"cz date", "10.01.2027", utc(2027, 1, 10, 0, 0, 0)},
		{"cz time", "01.02.2026 10:11:12", utc(2026, 2, 1, 10, 11, 12)},
		{"fi", "1.2.2026 10:11:12", utc(2026, 2, 1, 10, 11, 12)},
		{"pl", "2027.01.10 12:00:00", utc(2027, 1, 10, 12, 0, 0)},
		{"kr", "2027. 01. 10.", utc(2027, 1, 10, 0, 0, 0)},
		{"br", "20270522", utc(2027, 5, 22, 0, 0, 0)},
		{"at", "20260201 12:00:00", utc(2026, 2, 1, 12, 0, 0)},
		{"bd", "03/04/2027", utc(2027, 4, 3, 0, 0, 0)},
		{"pt", "03/02/2027 12:30:00", utc(2027, 2, 3, 12, 30, 0)},
		{"hk", "01-02-2026", utc(2026, 2, 1, 0, 0, 0)},
		{"tr", "2027-Jan-10.", utc(2027, 1, 10, 0, 0, 0)},
		{"be", "Mon Jan 10 2000", utc(2000, 1, 10, 0, 0, 0)},
		{"kg", "Sun Jan 10 12:00:00 2027", utc(2027, 1, 10, 12, 0, 0)},
		{"id", "10-Jan-2027 12:00:00 UTC", utc(2027, 1, 10, 12, 0, 0)},
		{"uk", "10-Jan-2027", utc(2027, 1, 10, 0, 0, 0)},
		{"gg", "7th June 2005 at 08:09:10.000000", utc(2005, 6, 7, 8, 9, 10)},
		{"kz", "2018-03-22 11:55:21 (GMT+0:00)", utc(2018, 3, 22, 11, 55, 21)},
		{"kz offset", "2018-03-22 11:55:21 (GMT+5:00)", utc(2018, 3, 22, 6, 55, 21)},
		{"pa", " 16 July 1999 ", utc(1999, 7, 16, 0, 0, 0)},
		{"month abbreviation", "25 Jun 2026", utc(2026, 6, 25, 0, 0, 0)},
		{"unix date", "Sun Jan 10 12:00:00 UTC 2027", utc(2027, 1, 10, 12, 0, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Time(tt.value)
			if !ok || !got.Equal(tt.want) {
				t.Fatalf("Time(%q) = %v, %v; want %v", tt.value, got, ok, tt.want)
			}
		})
	}
}

func TestTimeReadsNothingFromPlaceholders(t *testing.T) {
	for _, value := range []string{"", "  ", "N/A", "-", "--", "none", "Not defined.", "unknown", "someday", "2026-01-02 03:04:05 CET"} {
		if got, ok := Time(value); ok {
			t.Fatalf("Time(%q) = %v, want nothing", value, got)
		}
	}
}

// TestTimeReadsEveryFixtureDate runs every WHOIS fixture through the default
// registry and reads each date the parser left.
func TestTimeReadsEveryFixtureDate(t *testing.T) {
	dirs, err := os.ReadDir(fixturePath("whois"))
	if err != nil {
		t.Fatal(err)
	}
	registry := Default()
	read := 0
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		tld := dir.Name()
		files, err := filepath.Glob(fixturePath("whois", tld, "*.raw"))
		if err != nil {
			t.Fatal(err)
		}
		q := domainQueryFor("example."+tld, tld)
		if tld == "generic" {
			q = domainQuery("example.com")
		}
		for _, file := range files {
			part, err := registry.Parse(context.Background(), model.RawResponse{
				Source: model.SourceWHOIS,
				Query:  q.Query,
				Body:   readString(t, file),
			}, q)
			if err != nil {
				t.Fatal(err)
			}
			for field, value := range map[string]string{
				"created": part.Dates.CreatedAt,
				"updated": part.Dates.UpdatedAt,
				"expires": part.Dates.ExpiresAt,
			} {
				if value == "" {
					continue
				}
				if _, ok := Time(value); !ok {
					t.Errorf("%s: %s date %q not read", file, field, value)
				}
				read++
			}
		}
	}
	if read < 100 {
		t.Fatalf("read %d dates, want at least 100", read)
	}
}

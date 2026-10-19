package dnscheck

import (
	"slices"
	"testing"
)

func TestParseResolvers(t *testing.T) {
	got, err := ParseResolvers("223.5.5.5, 119.29.29.29:5353 [2400:3200::1]:53,2400:3200::1\tdns.example.com")
	want := []string{"223.5.5.5:53", "119.29.29.29:5353", "[2400:3200::1]:53", "[2400:3200::1]:53", "dns.example.com:53"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("= %v, %v", got, err)
	}
	if got, err := ParseResolvers(" "); err != nil || len(got) != 0 {
		t.Errorf("none = %v, %v", got, err)
	}
	for _, bad := range []string{"1.2.3.4:0", "1.2.3.4:dns", "not a resolver!", "-:53x", "a..b"} {
		if _, err := ParseResolvers(bad); err == nil {
			t.Errorf("%q passed", bad)
		}
	}
}

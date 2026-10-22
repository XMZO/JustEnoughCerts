package lookup

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// TestLive looks up real domains with their real registries, set
// JEC_TEST_WHOICE to names to look up, separated by commas, or to 1 for a
// list that touches each kind of source.
func TestLive(t *testing.T) {
	names := os.Getenv("JEC_TEST_WHOICE")
	if names == "" {
		t.Skip("JEC_TEST_WHOICE not set")
	}
	if names == "1" {
		names = "google.com,google.de,google.jp,google.cn,nic.ch,google.pl,google.cz,google.fi,google.co.kr,google.com.br,google.lk,nic.ni,nic.pa,nic.dz,dig.al,ka.uy,umi.li"
	}
	client, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	fetch, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	l := New(client, fetch)
	for name := range strings.SplitSeq(names, ",") {
		t.Run(name, func(t *testing.T) {
			r, err := l.Domain(t.Context(), name)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			end := "-"
			if !r.Expires.IsZero() {
				end = r.Expires.UTC().Format(time.DateOnly)
			}
			t.Logf("%-16s %-12s end %-10s via %-8s %s | %s | %s", name, r.Status, end, r.Source, r.Server, r.Registrar, r.Note)
		})
	}
}

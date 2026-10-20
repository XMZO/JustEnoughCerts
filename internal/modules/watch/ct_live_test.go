package watch

import (
	"os"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// TestLiveCertSpotter searches the real Cert Spotter for the domain in
// JEC_TEST_CERTSPOTTER, as getjec.dev, with the API key in
// JEC_TEST_CERTSPOTTER_KEY if any, and reads two pages.
func TestLiveCertSpotter(t *testing.T) {
	domain := os.Getenv("JEC_TEST_CERTSPOTTER")
	if domain == "" {
		t.Skip("JEC_TEST_CERTSPOTTER not set")
	}
	client, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	s := &spotter{client: client, url: certSpotterURL}
	key := os.Getenv("JEC_TEST_CERTSPOTTER_KEY")
	page, err := s.search(t.Context(), domain, "", key)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) == 0 {
		t.Fatalf("no certificates for %s", domain)
	}
	for _, c := range page {
		if c.ID == "" || len(c.DNSNames) == 0 || len(c.Pubkey) != 64 || len(c.SHA256) != 64 || c.issuer() == "" || !c.NotAfter.After(c.NotBefore) {
			t.Errorf("certificate %+v", c)
		}
	}
	t.Logf("%d certificates, the first %v by %s", len(page), page[0].DNSNames, page[0].issuer())
	next, err := s.search(t.Context(), domain, page[len(page)-1].ID, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range next {
		if c.ID <= page[len(page)-1].ID && len(c.ID) == len(page[len(page)-1].ID) {
			t.Errorf("the next page goes back to %s", c.ID)
		}
	}
	t.Logf("the next page: %d", len(next))
}

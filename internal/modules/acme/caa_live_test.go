package acme

import (
	"os"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

// TestLivePreflightCAA reads real CAA records, when JEC_TEST_CAA is set:
// google.com lets only its own CA issue, not Let's Encrypt.
func TestLivePreflightCAA(t *testing.T) {
	if os.Getenv("JEC_TEST_CAA") == "" {
		t.Skip("JEC_TEST_CAA not set")
	}
	got := preflightCAA(t.Context(), dnscheck.CAA, "https://acme-v02.api.letsencrypt.org/directory", []string{"google.com", "letsencrypt.org"})
	if len(got) != 1 || got[0].Params["domain"] != "google.com" {
		t.Fatalf("problems %+v", got)
	}
	t.Logf("%s %v", got[0].Key, got[0].Params)
}

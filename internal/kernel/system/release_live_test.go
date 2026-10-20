package system

import (
	"net/http"
	"os"
	"testing"
	"time"
)

// TestLiveRelease asks the real GitHub for the releases of JEC, when
// JEC_TEST_GITHUB is set: a test release finds a newer one or itself.
func TestLiveRelease(t *testing.T) {
	if os.Getenv("JEC_TEST_GITHUB") == "" {
		t.Skip("JEC_TEST_GITHUB not set")
	}
	body, err := checkRelease(t.Context(), &http.Client{Timeout: 30 * time.Second}, "0.1.0-alpha.1")
	if err != nil {
		t.Fatal(err)
	}
	if !body.Newer || body.Latest == "" || body.URL == "" || body.PublishedAt == nil {
		t.Fatalf("release %+v", body)
	}
	t.Logf("latest %s at %s", body.Latest, body.URL)
}

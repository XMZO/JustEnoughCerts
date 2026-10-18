package system

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// releases of the tests, as GitHub lists them: the newest first, and not
// in the order of their versions.
var testReleases = []release{
	{Tag: "v0.2.0", Draft: true},
	{Tag: "v0.1.0-alpha.10", URL: "https://example.com/a10", Prerelease: true, PublishedAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)},
	{Tag: "nightly", Prerelease: true},
	{Tag: "v0.1.0-alpha.4", URL: "https://example.com/a4", Prerelease: true},
	{Tag: "v0.0.1", URL: "https://example.com/001"},
}

func TestLatest(t *testing.T) {
	for current, want := range map[string]string{
		// A stable version moves to stable releases only.
		"0.0.1": "v0.0.1",
		// A test release, or a build of its own, to any.
		"0.1.0-alpha.4": "v0.1.0-alpha.10",
		"dev":           "v0.1.0-alpha.10",
	} {
		if r, ok := latest(current, testReleases); !ok || r.Tag != want {
			t.Errorf("%s: %+v %v", current, r, ok)
		}
	}
	if _, ok := latest("0.0.1", testReleases[:4]); ok {
		t.Error("a stable version found a test release")
	}
}

func TestCheckRelease(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("accept %q", r.Header.Get("Accept"))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`[{"tag_name": "v0.2.0", "draft": true},
			{"tag_name": "v0.1.0-alpha.10", "html_url": "https://example.com/a10", "prerelease": true, "published_at": "2026-10-20T00:00:00Z"},
			{"tag_name": "v0.0.1", "html_url": "https://example.com/001", "published_at": "2026-10-17T00:00:00Z"}]`))
	}))
	defer srv.Close()
	old := releasesURL
	releasesURL = srv.URL
	t.Cleanup(func() { releasesURL = old })

	for _, c := range []struct {
		current, latest string
		newer           bool
	}{
		{"0.1.0-alpha.4", "0.1.0-alpha.10", true},
		{"0.1.0-alpha.10", "0.1.0-alpha.10", false},
		{"0.0.1", "0.0.1", false},
		{"dev", "0.1.0-alpha.10", false}, // newer or not, it cannot say
	} {
		got, err := checkRelease(t.Context(), srv.Client(), c.current)
		if err != nil || got.Current != c.current || got.Latest != c.latest || got.Newer != c.newer || got.URL == "" || got.PublishedAt == nil {
			t.Errorf("%s: %+v %v", c.current, got, err)
		}
	}
	status = http.StatusForbidden // rate limited
	if _, err := checkRelease(t.Context(), srv.Client(), "0.0.1"); err == nil {
		t.Error("a refusal checked out")
	}
}

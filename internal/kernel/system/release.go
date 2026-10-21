package system

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/mod/semver"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

// repository is where JEC is released.
const repository = "XMZO/JustEnoughCerts"

// ReleasesURL lists the releases of the repository, the newest first.
const ReleasesURL = "https://api.github.com/repos/" + repository + "/releases?per_page=30"

// DownloadURL is where the files of a release are, by tag and file name.
const DownloadURL = "https://github.com/" + repository + "/releases/download"

// releasesURL is ReleasesURL, but for tests.
var releasesURL = ReleasesURL

// ReleaseBody tells whether a newer release than the running one is out.
type ReleaseBody struct {
	Current string `json:"current" doc:"The running version"`
	// Latest is empty when there is no release to compare with.
	Latest      string     `json:"latest,omitempty" doc:"The newest release this version may move to: stable ones only, unless this one is a test release or a build of its own"`
	Newer       bool       `json:"newer" doc:"Latest is newer than the running version"`
	URL         string     `json:"url,omitempty" doc:"The page of the latest release"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
}

// release is a release as GitHub lists it.
type release struct {
	Tag         string    `json:"tag_name"`
	URL         string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

// latest picks the newest of releases that current may move to: a stable
// release for a stable version, any release for a test release or for a
// build without a version, as "dev".
func latest(current string, releases []release) (release, bool) {
	cur := "v" + strings.TrimPrefix(current, "v")
	anyOne := !semver.IsValid(cur) || semver.Prerelease(cur) != ""
	var best release
	found := false
	for _, r := range releases {
		if r.Draft || !semver.IsValid(r.Tag) || (r.Prerelease && !anyOne) || (semver.Prerelease(r.Tag) != "" && !anyOne) {
			continue
		}
		if !found || semver.Compare(r.Tag, best.Tag) > 0 {
			best, found = r, true
		}
	}
	return best, found
}

// checkRelease asks GitHub for the releases and tells whether one is newer
// than current. It only runs when someone asks: JEC calls home on its own
// never.
func checkRelease(ctx context.Context, client *http.Client, current string) (ReleaseBody, error) {
	return LatestRelease(ctx, client, releasesURL, current)
}

// LatestRelease asks list, a listing of releases as ReleasesURL gives, for
// the newest release current may move to, and tells whether it is newer.
func LatestRelease(ctx context.Context, client *http.Client, list, current string) (ReleaseBody, error) {
	out := ReleaseBody{Current: current}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, list, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("system: GitHub answered HTTP %d", resp.StatusCode)
	}
	var releases []release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&releases); err != nil {
		return out, fmt.Errorf("system: the releases: %w", err)
	}
	r, ok := latest(current, releases)
	if !ok {
		return out, nil
	}
	out.Latest, out.URL = strings.TrimPrefix(r.Tag, "v"), r.URL
	published := r.PublishedAt
	out.PublishedAt = &published
	// A build without a version cannot tell whether a release is newer.
	cur := "v" + strings.TrimPrefix(current, "v")
	out.Newer = semver.IsValid(cur) && semver.Compare(r.Tag, cur) > 0
	return out, nil
}

func registerRelease(a huma.API, client *http.Client, current string, tags []string) {
	huma.Register(a, huma.Operation{
		OperationID: "system-release", Method: http.MethodGet, Path: "/system/release", Tags: tags,
		Summary:     "Tell whether a newer release of JEC is out",
		Description: "Asks GitHub for the releases of JEC, only when called.",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body ReleaseBody }, error) {
		body, err := checkRelease(ctx, client, current)
		if err != nil {
			return nil, api.NewError(http.StatusBadGateway, i18n.KernelErrorReleaseCheck, nil)
		}
		return &struct{ Body ReleaseBody }{body}, nil
	})
}

package certs

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

func TestWindowStart(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 10, 17, h, 30, 0, 0, time.UTC) }
	day := func(h int) time.Time { return time.Date(2026, 10, 17, h, 0, 0, 0, time.UTC) }
	for _, c := range []struct {
		window   string
		now, due time.Time
	}{
		{"", at(14), at(14)},
		{"1-5", at(14), day(1).AddDate(0, 0, 1)},
		{"1-5", at(3), at(3)},
		{"1-5", at(0), day(1)},
		{"22-6", at(23), at(23)},
		{"22-6", at(2), at(2)},
		{"22-6", at(14), day(22)},
		{"bad", at(14), at(14)},
	} {
		if got := windowStart(c.window, c.now, time.UTC); !got.Equal(c.due) {
			t.Errorf("%q at %s: %s, want %s", c.window, c.now.Format("15:04"), got, c.due)
		}
	}
}

func TestDeployWindow(t *testing.T) {
	f := newFixture(t)
	f.svc.zone = func() *time.Location { return time.UTC }
	target := f.provider("fake.hook")
	c := f.uploadTo(f.leaf(60, "example.com"), target)
	f.runDeploys()
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)
	var got CertificateBody
	f.call(f.api.Patch(path, map[string]any{"deployWindow": "1-5"}), http.StatusOK, &got)
	if got.DeployWindow != "1-5" {
		t.Fatalf("window %q", got.DeployWindow)
	}

	// A new version at 08:00 waits for 01:00, the certificate fine meanwhile.
	f.uploadVersion(c.ID, f.leaf(90, "example.com"))
	before := len(f.fake.deployed)
	f.runDeploys()
	got = f.certificate(c.ID)
	if len(f.fake.deployed) != before || got.State != "ok" {
		t.Fatalf("deployed at once: %d, %s", len(f.fake.deployed)-before, got.State)
	}
	var due time.Time
	for _, d := range got.Deployments {
		if d.State == "pending" && d.NextAt != nil {
			due = *d.NextAt
		}
	}
	if want := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC); !due.Equal(want) {
		t.Errorf("due at %s, want %s", due, want)
	}
	// The monitor is told when it goes out.
	var deployAt time.Time
	for _, e := range f.events() {
		if e.typ == contracts.CertificateIssued {
			deployAt = e.data.DeployAt
		}
	}
	if !deployAt.Equal(due) {
		t.Errorf("told the deployment at %s", deployAt)
	}
	// By hand, at once; in the window, at once too.
	f.call(f.api.Post(path+"/deploy", map[string]any{}), http.StatusAccepted, nil)
	f.runDeploys()
	if len(f.fake.deployed) != before+1 {
		t.Errorf("by hand: %d deployments", len(f.fake.deployed)-before)
	}
}

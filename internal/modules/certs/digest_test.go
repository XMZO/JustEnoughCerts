package certs

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

func TestDigestOf(t *testing.T) {
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	version := func(days int) *Version {
		return &Version{CreatedAt: now.AddDate(0, 0, -60), NotBefore: now.AddDate(0, 0, days-90), NotAfter: now.AddDate(0, 0, days)}
	}
	certs := []*Certificate{
		{ID: 1, Domains: []string{"ok.example.com"}, Source: sourceUpload, RenewBeforeDays: 30, Current: version(90)},
		// Uploaded, ending soon: no source renews it.
		{ID: 2, Domains: []string{"upload.example.com"}, Source: sourceUpload, RenewBeforeDays: 30, Current: version(10)},
		// Renewed by its source within the week, which ends in 35 days.
		{ID: 3, Domains: []string{"auto.example.com"}, Source: sourceProvider, RenewBeforeDays: 30, Current: version(35)},
		{ID: 4, Domains: []string{"failed.example.com"}, Source: sourceProvider, RenewBeforeDays: 30, Current: version(80),
			Issuance: &Issuance{State: issuanceFailed, CreatedAt: now}},
		{ID: 5, Domains: []string{"gone.example.com"}, Source: sourceUpload, RenewBeforeDays: 30, Current: version(-1)},
	}
	d := digestOf(certs, now, schedule{checkTime: 2 * 60, loc: time.UTC})
	var attention, renewals []string
	for _, it := range d.Attention {
		attention = append(attention, it.Domain+" "+it.State)
	}
	for _, it := range d.Renewals {
		renewals = append(renewals, it.Domain)
	}
	if d.Total != 5 || d.OK != 2 ||
		!slices.Equal(attention, []string{"failed.example.com failed", "gone.example.com expired", "upload.example.com expiring"}) ||
		!slices.Equal(renewals, []string{"auto.example.com"}) || d.Renewals[0].RenewAt.IsZero() {
		t.Errorf("digest %+v: attention %v, renewals %v", d, attention, renewals)
	}
}

func TestDigestWeekly(t *testing.T) {
	f := newFixture(t)
	f.add(f.leaf(90, "example.com"))
	digests := func() int {
		n := 0
		for _, e := range f.events() {
			if e.typ == contracts.CertificatesDigest {
				n++
			}
		}
		return n
	}
	// start is a Saturday; Monday comes, before nine and from nine.
	for _, c := range []struct {
		at   time.Time
		want int
	}{
		{start, 0},
		{time.Date(2026, 10, 19, 8, 59, 0, 0, time.UTC), 0},
		{time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC), 1},
		{time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC), 1}, // told this week already
		{time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC), 2},
	} {
		f.now = c.at
		if err := f.svc.digest(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := digests(); got != c.want {
			t.Errorf("at %v: %d digests, want %d", c.at, got, c.want)
		}
	}
	var data string
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM kernel_events WHERE type = ?", contracts.CertificatesDigest).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var d contracts.Digest
	if err := json.Unmarshal([]byte(data), &d); err != nil || d.Total != 1 || d.OK != 1 {
		t.Errorf("payload %s: %v", data, err)
	}
}

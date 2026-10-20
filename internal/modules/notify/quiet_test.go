package notify

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
)

func TestQuietUntil(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 17, h, m, 0, 0, time.UTC) }
	for _, c := range []struct {
		quiet string
		now   time.Time
		until time.Time // zero: not quiet
	}{
		{"22-7", at(21, 59), time.Time{}},
		{"22-7", at(22, 0), at(7, 0).AddDate(0, 0, 1)},
		{"22-7", at(3, 30), at(7, 0)},
		{"22-7", at(7, 0), time.Time{}},
		{"0-8", at(0, 0), at(8, 0)},
		{"0-8", at(8, 0), time.Time{}},
		{"0-8", at(23, 0), time.Time{}},
		{"off", at(23, 0), time.Time{}},
		{"", at(23, 0), time.Time{}},
		{"7-7", at(7, 0), time.Time{}},
	} {
		until, quiet := quietUntil(map[string]string{contracts.NoteQuiet: c.quiet}, c.now, time.UTC)
		if quiet != !c.until.IsZero() || !until.Equal(c.until) {
			t.Errorf("%q at %s: %v %s, want %s", c.quiet, c.now.Format("15:04"), quiet, until, c.until)
		}
	}
	// In the panel's time zone.
	shanghai := time.FixedZone("CST", 8*3600)
	if until, quiet := quietUntil(map[string]string{contracts.NoteQuiet: "22-7"}, at(15, 0), shanghai); !quiet || !until.Equal(at(23, 0)) {
		t.Errorf("15:00 UTC in Shanghai: %v %s", quiet, until)
	}
}

func TestQuietHours(t *testing.T) {
	f := newFixture(t)
	quiet := f.notifier("night", map[string]string{"locale": "en-US", contracts.NoteQuiet: "22-7"})
	loud := f.notifier("day", map[string]string{"locale": "en-US"})
	now := time.Date(2026, 10, 17, 23, 0, 0, 0, time.UTC)
	f.svc.now = func() time.Time { return now }
	d := contracts.SiteEvent{SiteID: 1, Host: "example.com", Port: 443, NotAfter: now.Add(24 * time.Hour), Expiry: contracts.ExpiryUrgent, Issuer: "R12"}
	raw, _ := json.Marshal(d)
	if err := f.svc.handle(t.Context(), events.Event{ID: 1, Type: contracts.SiteExpiring, Source: "watch", Subject: "1", Data: raw, Time: now}); err != nil {
		t.Fatal(err)
	}
	if len(f.notes(loud)) != 1 || len(f.notes(quiet)) != 0 {
		t.Fatalf("at night: loud %d, quiet %d", len(f.notes(loud)), len(f.notes(quiet)))
	}
	sends, err := f.svc.latest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range sends {
		if x.Provider == quiet && (x.State != stateHeld || !x.SendAt.Equal(time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC))) {
			t.Errorf("held %+v", x)
		}
	}
	// Delivered again, the event is not held twice; released at the end.
	if err := f.svc.handle(t.Context(), events.Event{ID: 1, Type: contracts.SiteExpiring, Source: "watch", Subject: "1", Data: raw, Time: now}); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 10, 18, 6, 59, 0, 0, time.UTC)
	if err := f.svc.release(t.Context()); err != nil || len(f.notes(quiet)) != 0 {
		t.Fatalf("released early: %v %d", err, len(f.notes(quiet)))
	}
	// A failure is tried again a while later.
	f.fails[quiet] = errors.New("chat down")
	now = now.Add(time.Minute)
	if err := f.svc.release(t.Context()); err != nil || len(f.notes(quiet)) != 0 {
		t.Fatalf("failed: %v %d", err, len(f.notes(quiet)))
	}
	delete(f.fails, quiet)
	now = now.Add(heldRetry)
	if err := f.svc.release(t.Context()); err != nil {
		t.Fatal(err)
	}
	notes := f.notes(quiet)
	if len(notes) != 1 || notes[0].Event != contracts.SiteExpiring || !notes[0].Time.Equal(time.Date(2026, 10, 17, 23, 0, 0, 0, time.UTC)) {
		t.Fatalf("released %+v", notes)
	}
	if err := f.svc.release(t.Context()); err != nil || len(f.notes(quiet)) != 1 {
		t.Fatalf("released twice: %v %d", err, len(f.notes(quiet)))
	}
	sends, _ = f.svc.latest(t.Context())
	for _, x := range sends {
		if x.Provider == quiet && x.State != stateSent {
			t.Errorf("after release %+v", x)
		}
	}
}

package certs

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

func TestRenewAt(t *testing.T) {
	utc := schedule{checkTime: 2 * 60, loc: time.UTC}
	end := time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name   string
		sch    schedule
		before int
		want   time.Time
	}{
		{"the check time after entering the window", utc, 30, time.Date(2026, 12, 2, 2, 0, 0, 0, time.UTC)},
		{"a window entered at the check time", schedule{checkTime: 12 * 60, loc: time.UTC}, 30, time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)},
		{"the check time before the last week", utc, 8, time.Date(2026, 12, 24, 2, 0, 0, 0, time.UTC)},
		{"a window of the last week", utc, 7, time.Date(2026, 12, 24, 12, 0, 0, 0, time.UTC)},
		{"a window within the last week", utc, 3, time.Date(2026, 12, 28, 12, 0, 0, 0, time.UTC)},
		{"in the server's time zone", schedule{checkTime: 2 * 60, loc: time.FixedZone("CST", 8*3600)}, 30, time.Date(2026, 12, 1, 18, 0, 0, 0, time.UTC)},
	} {
		if got := c.sch.renewAt(end, c.before); !got.Equal(c.want) {
			t.Errorf("%s: %s, want %s", c.name, got.UTC(), c.want)
		}
	}

	// 02:30 does not exist the day New York's clocks go forward: either
	// side of the gap will do.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	window := time.Date(2027, 3, 14, 5, 0, 0, 0, time.UTC) // midnight there
	got := schedule{checkTime: 2*60 + 30, loc: ny}.renewAt(window.Add(30*day), 30)
	if got.Before(window) || got.After(window.Add(4*time.Hour)) {
		t.Errorf("across the gap: %s", got)
	}
}

func TestSettings(t *testing.T) {
	f := newFixture(t)
	var st CertSettingsBody
	f.call(f.api.Get("/settings"), http.StatusOK, &st)
	if st != (CertSettingsBody{RenewBeforeDays: 30, KeyType: "rsa-2048", CheckTime: "02:00", TimeZone: "UTC", Retries: defaultRetries}) {
		t.Errorf("defaults %+v", st)
	}
	want := CertSettingsBody{RenewBeforeDays: 45, KeyType: "ecdsa-p384", CheckTime: "23:30", TimeZone: "UTC", Retries: 3}
	f.call(f.api.Put("/settings", map[string]any{"renewBeforeDays": 45, "keyType": "ecdsa-p384", "checkTime": "23:30", "retries": 3}), http.StatusOK, &st)
	if st != want {
		t.Errorf("saved %+v", st)
	}
	f.call(f.api.Get("/settings"), http.StatusOK, &st)
	if st != want {
		t.Errorf("then %+v", st)
	}
	// Left out, the retries stay as they are.
	f.call(f.api.Put("/settings", map[string]any{"renewBeforeDays": 45, "keyType": "ecdsa-p384", "checkTime": "23:30"}), http.StatusOK, &st)
	if st != want {
		t.Errorf("without the retries %+v", st)
	}
	for _, bad := range []map[string]any{
		{"renewBeforeDays": 45, "keyType": "ecdsa-p384", "checkTime": "24:00"},
		{"renewBeforeDays": 45, "keyType": "ecdsa-p384", "checkTime": "3:30"},
		{"renewBeforeDays": 0, "keyType": "ecdsa-p384", "checkTime": "03:30"},
		{"renewBeforeDays": 45, "keyType": "dsa", "checkTime": "03:30"},
		{"renewBeforeDays": 45, "keyType": "ecdsa-p384"},
		{"renewBeforeDays": 45, "keyType": "ecdsa-p384", "checkTime": "03:30", "retries": -1},
		{"renewBeforeDays": 45, "keyType": "ecdsa-p384", "checkTime": "03:30", "retries": maxRetries + 1},
	} {
		f.call(f.api.Put("/settings", bad), http.StatusUnprocessableEntity, nil)
	}

	// New certificates take the defaults.
	var c CertificateBody
	f.call(f.api.Post("/certificates/issue", map[string]any{
		"domains": []string{"example.com"}, "sourceProvider": f.provider("fake.ca"), "solverProvider": f.provider("fake.dns"),
	}), http.StatusCreated, &c)
	if c.KeyType != "ecdsa-p384" || c.RenewBeforeDays != 45 {
		t.Errorf("issued %+v", c)
	}
	if up := f.add(f.leaf(90, "upload.example.com")); up.RenewBeforeDays != 45 || up.RenewAt != nil {
		t.Errorf("uploaded %+v", up)
	}
}

func TestRenewAtCheckTime(t *testing.T) {
	f := newFixture(t)
	f.call(f.api.Put("/settings", map[string]any{"renewBeforeDays": 30, "keyType": "rsa-2048", "checkTime": "03:00"}), http.StatusOK, nil)
	c := f.issueNew("example.com")
	f.run()
	c = f.certificate(c.ID)
	want := nextClock(c.Current.NotAfter.Add(-30*day), 3*60, time.UTC)
	if c.RenewAt == nil || !c.RenewAt.Equal(want) {
		t.Fatalf("renewAt %v, want %s", c.RenewAt, want)
	}

	// In the renewal window, but before the check time: not yet.
	f.now = want.Add(-time.Minute)
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.certificate(c.ID); got.Issuance.Reason != "first" || got.State != "expiring" {
		t.Fatalf("before the check time: %s, %s", got.Issuance.Reason, got.State)
	}
	f.now = want
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.certificate(c.ID).Issuance; got.Reason != "renewal" {
		t.Errorf("at the check time: %+v", got)
	}
}

func TestRetriesSetting(t *testing.T) {
	f := newFixture(t)
	f.call(f.api.Put("/settings", map[string]any{"renewBeforeDays": 30, "keyType": "rsa-2048", "checkTime": "02:00", "retries": 1}), http.StatusOK, nil)
	c := f.issueNew("example.com")
	f.failWith(&contracts.Problem{Key: i18n.AcmeErrorEabRequired})
	f.run()
	is := f.certificate(c.ID).Issuance
	if is.State != "pending" || is.NextAt == nil {
		t.Fatalf("after the first attempt: %+v", is)
	}
	f.now = *is.NextAt
	f.run()
	if is := f.certificate(c.ID).Issuance; is.State != "failed" || is.Attempts != 2 {
		t.Errorf("after the one retry: %+v", is)
	}

	// None at all.
	final, _ := nextAttempt(1, 0, errors.New("down"), f.now)
	if !final {
		t.Error("retried with no retries")
	}
	// The wait is the backoff's, which takes off up to half at random.
	if final, next := nextAttempt(1, 1, errors.New("down"), f.now); final || next.Before(f.now.Add(retryBase/2)) || next.After(f.now.Add(retryBase)) {
		t.Errorf("the one retry: %v at %v", final, next)
	}
}

package certs

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

// suggest has the fake CA suggest renewing in [start, end).
func (f *fixture) suggest(start, end time.Time, retryAfter time.Duration, explanation string) {
	f.fake.mu.Lock()
	defer f.fake.mu.Unlock()
	f.fake.renewal = func(*x509.Certificate) (contracts.RenewalInfo, error) {
		return contracts.RenewalInfo{Start: start, End: end, RetryAfter: retryAfter, ExplanationURL: explanation}, nil
	}
}

func (f *fixture) asked() int {
	f.fake.mu.Lock()
	defer f.fake.mu.Unlock()
	return f.fake.asked
}

func (f *fixture) checkRenewalInfo() {
	f.t.Helper()
	if err := f.svc.checkRenewalInfo(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
}

func TestRenewalInfo(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.run()
	f.add(f.leaf(10, "upload.example.com"))
	scheduled := f.certificate(c.ID).RenewAt

	// A CA without suggestions is asked again in a week; an uploaded
	// certificate is never asked.
	f.checkRenewalInfo()
	if f.asked() != 1 {
		t.Fatalf("asked %d times", f.asked())
	}
	f.now = f.now.Add(6 * day)
	f.checkRenewalInfo()
	if f.asked() != 1 {
		t.Fatalf("asked again after %d times", f.asked())
	}
	f.now = f.now.Add(day)

	// A window before the schedule moves the renewal into it, at a time
	// that stays while the window does; the CA's delay is kept.
	start := f.now.Add(10 * day)
	f.suggest(start, start.Add(day), 2*time.Hour, "javascript:alert(1)")
	f.checkRenewalInfo()
	got := f.certificate(c.ID)
	w := got.RenewalWindow
	if w == nil || !w.Start.Equal(start) || !w.End.Equal(start.Add(day)) || w.ExplanationURL != "" ||
		got.RenewAt == nil || got.RenewAt.Before(start) || !got.RenewAt.Before(start.Add(day)) {
		t.Fatalf("window %+v, renewal at %v", w, got.RenewAt)
	}
	at := *got.RenewAt
	f.checkRenewalInfo()
	f.now = f.now.Add(2 * time.Hour)
	f.checkRenewalInfo()
	if f.asked() != 3 || !f.certificate(c.ID).RenewAt.Equal(at) {
		t.Errorf("asked %d times, renewal at %v, then %v", f.asked(), at, f.certificate(c.ID).RenewAt)
	}

	// A window after the schedule does not hold the renewal back.
	f.suggest(scheduled.Add(day), scheduled.Add(2*day), 0, "https://ca.example/why")
	f.now = f.now.Add(6 * time.Hour)
	f.checkRenewalInfo()
	if got := f.certificate(c.ID); !got.RenewAt.Equal(*scheduled) || got.RenewalWindow.ExplanationURL != "https://ca.example/why" {
		t.Errorf("renewal at %v, not %v; window %+v", got.RenewAt, scheduled, got.RenewalWindow)
	}

	// A window in the past, as before a revocation, renews at once, and the
	// order says it replaces the current version.
	f.suggest(f.now.Add(-day), f.now.Add(-time.Hour), 0, "")
	f.now = f.now.Add(6 * time.Hour)
	f.checkRenewalInfo()
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if is := f.certificate(c.ID).Issuance; is.Reason != "renewal" {
		t.Fatalf("no renewal: %+v", is)
	}
	current := f.certificate(c.ID).Current
	var replaces *x509.Certificate
	f.fake.mu.Lock()
	f.fake.issue = func(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
		replaces = req.Replaces
		return f.issueAll(ctx, req)
	}
	f.fake.mu.Unlock()
	f.run()
	if replaces == nil || replaces.SerialNumber.Text(16) != current.Serial {
		t.Errorf("replaces %v, not %s", replaces, current.Serial)
	}
	if got := f.certificate(c.ID); got.Current.ID == current.ID || got.RenewalWindow != nil {
		t.Errorf("after the renewal: %+v", got)
	}
}

func TestRenewalInfoFailure(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.run()
	f.fake.mu.Lock()
	f.fake.renewal = func(*x509.Certificate) (contracts.RenewalInfo, error) {
		return contracts.RenewalInfo{}, errors.New("unreachable")
	}
	f.fake.mu.Unlock()
	f.checkRenewalInfo()
	f.now = f.now.Add(30 * time.Minute)
	f.checkRenewalInfo()
	if f.asked() != 1 {
		t.Errorf("asked %d times within the hour", f.asked())
	}
	f.now = f.now.Add(30 * time.Minute)
	f.checkRenewalInfo()
	if f.asked() != 2 || f.certificate(c.ID).RenewalWindow != nil {
		t.Errorf("asked %d times after an hour", f.asked())
	}
	// A version from another source is neither asked about nor replaced.
	if _, err := f.svc.db.ExecContext(t.Context(), "UPDATE certs_certificates SET source_provider = ? WHERE id = ?", f.provider("fake.ca"), c.ID); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(day)
	f.checkRenewalInfo()
	if f.asked() != 2 {
		t.Errorf("asked about a version of another source")
	}
}

package audit

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

func newModule(t *testing.T) *Module {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New()
	if _, err := st.Migrate(t.Context(), "audit", m.Migrations()); err != nil {
		t.Fatal(err)
	}
	m.db, m.log = st.DB(), slog.New(slog.DiscardHandler)
	return m
}

func TestLog(t *testing.T) {
	m := newModule(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	for i, at := range []time.Time{now.Add(-400 * 24 * time.Hour), now.Add(-10 * 24 * time.Hour), now.Add(-time.Hour), now.Add(-time.Hour)} {
		m.record(t.Context(), module.Request{At: at, Operation: "certs-delete", Method: "DELETE", Path: "/certs/certificates/" + string(rune('1'+i)),
			Status: 204, User: "admin", Client: "198.51.100.1", UserAgent: strings.Repeat("x", 600)})
	}

	all, more, err := m.entries(t.Context(), time.Time{}, 10)
	if err != nil || more || len(all) != 4 {
		t.Fatalf("%d entries, more %v: %v", len(all), more, err)
	}
	// The newest first; of two at once, the later recorded.
	if all[0].path != "/certs/certificates/4" || all[3].path != "/certs/certificates/1" || len(all[0].agent) != maxUserAgent ||
		all[0].user != "admin" || all[0].client != "198.51.100.1" || all[0].status != 204 || !all[0].at.Equal(now.Add(-time.Hour)) {
		t.Errorf("entries %+v", all)
	}
	week, more, err := m.entries(t.Context(), now.Add(-7*24*time.Hour), 1)
	if err != nil || !more || len(week) != 1 {
		t.Errorf("a week, one at most: %d, more %v: %v", len(week), more, err)
	}

	if err := m.prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	if left, _, _ := m.entries(t.Context(), time.Time{}, 10); len(left) != 3 {
		t.Errorf("%d left after a year", len(left))
	}
}

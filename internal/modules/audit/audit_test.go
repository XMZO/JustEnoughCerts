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
			Status: 204, User: "admin", Client: "198.51.100.1", UserAgent: strings.Repeat("x", 600), Token: "ci", Subject: "site" + string(rune('1'+i)) + ".example.com"})
	}

	all, counts, err := m.find(t.Context(), query{limit: 10})
	if err != nil || len(all) != 4 || counts != (tally{all: 4, ok: 4}) {
		t.Fatalf("%d entries, %+v: %v", len(all), counts, err)
	}
	// The newest first; of two at once, the later recorded.
	if all[0].path != "/certs/certificates/4" || all[3].path != "/certs/certificates/1" || len(all[0].agent) != maxUserAgent ||
		all[0].user != "admin" || all[0].client != "198.51.100.1" || all[0].status != 204 || !all[0].at.Equal(now.Add(-time.Hour)) || all[0].token != "ci" || all[0].subject != "site4.example.com" {
		t.Errorf("entries %+v", all)
	}
	week, counts, err := m.find(t.Context(), query{since: now.Add(-7 * 24 * time.Hour), limit: 1, offset: 1})
	if err != nil || len(week) != 1 || week[0].path != "/certs/certificates/3" || counts.all != 2 {
		t.Errorf("the second of a week's: %+v, %+v: %v", week, counts, err)
	}

	// A refusal, then the filters: by result, and by text in the path,
	// the address or the operation's name.
	m.record(t.Context(), module.Request{At: now, Operation: "sign-in", Method: "POST", Path: "/session", Status: 401, Client: "203.0.113.9"})
	for _, c := range []struct {
		q     query
		paths []string
		count tally
	}{
		{query{result: "failed"}, []string{"/session"}, tally{all: 5, ok: 4, failed: 1}},
		{query{result: "ok", text: "203.0"}, nil, tally{all: 1, failed: 1}},
		{query{text: "CERTIFICATES/2"}, []string{"/certs/certificates/2"}, tally{all: 1, ok: 1}},
		{query{text: "site3.EXAMPLE"}, []string{"/certs/certificates/3"}, tally{all: 1, ok: 1}},
		{query{text: "a name", ops: []string{"sign-in"}}, []string{"/session"}, tally{all: 1, failed: 1}},
		{query{text: "%"}, nil, tally{}},
	} {
		c.q.limit = 10
		got, counts, err := m.find(t.Context(), c.q)
		var paths []string
		for _, e := range got {
			paths = append(paths, e.path)
		}
		if err != nil || strings.Join(paths, " ") != strings.Join(c.paths, " ") || counts != c.count {
			t.Errorf("%+v: %v, %+v: %v", c.q, paths, counts, err)
		}
	}

	if err := m.prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	if left, _, _ := m.find(t.Context(), query{limit: 10}); len(left) != 4 {
		t.Errorf("%d left after a year", len(left))
	}
}

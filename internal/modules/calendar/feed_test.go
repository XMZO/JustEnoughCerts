package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

func newModule(t *testing.T) (*Module, humatest.TestAPI) {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New()
	if _, err := st.Migrate(t.Context(), "calendar", m.Migrations()); err != nil {
		t.Fatal(err)
	}
	cat, err := i18n.Default()
	if err != nil {
		t.Fatal(err)
	}
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	m.db, m.cat, m.baseURL = st.DB(), cat, "https://certs.example.com/"
	m.zone = func() *time.Location { return shanghai }
	m.now = func() time.Time { return time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC) }
	long := strings.Repeat("sub.", 15) + "example.com"
	m.call = func(_ context.Context, via, method, path string, _, out any) error {
		if via != "calendar" || method != http.MethodGet || path != "/certs/certificates" {
			t.Errorf("asked %s %s as %s", method, path, via)
		}
		return json.Unmarshal([]byte(`{"certificates": [
			{"id": 3, "domains": ["example.com", "*.example.com", "`+long+`"], "renewAt": "2026-12-01T18:00:00Z",
			 "current": {"notAfter": "2026-12-31T20:00:00Z"}},
			{"id": 4, "domains": ["new.example.com"]}]}`), out)
	}
	_, a := humatest.New(t)
	register(a, m)
	return m, a
}

func TestFeed(t *testing.T) {
	m, a := newModule(t)
	var feed FeedBody
	get := func() {
		t.Helper()
		resp := a.Get("/feed")
		if err := json.Unmarshal(resp.Body.Bytes(), &feed); err != nil {
			t.Fatal(err)
		}
	}
	get()
	if feed.Enabled {
		t.Fatalf("on before it was turned on: %+v", feed)
	}
	// Off, there is nothing to subscribe to.
	if resp := a.Get("/ics/anything"); resp.Code != http.StatusNotFound {
		t.Errorf("a feed while off: %d", resp.Code)
	}

	a.Put("/feed", map[string]any{"enabled": true})
	get()
	first := feed.Path
	if !feed.Enabled || !strings.HasPrefix(first, "/api/v1/calendar/ics/") || len(first) < len("/api/v1/calendar/ics/")+40 {
		t.Fatalf("turned on: %+v", feed)
	}
	// Turned on again, it keeps its URL; rotated, it gets a new one.
	a.Put("/feed", map[string]any{"enabled": true})
	if get(); feed.Path != first {
		t.Errorf("turning on again moved the feed to %s", feed.Path)
	}
	a.Post("/feed/rotate", map[string]any{})
	if get(); feed.Path == first {
		t.Error("rotating kept the URL")
	}
	if resp := a.Get(strings.TrimPrefix(first, "/api/v1/calendar")); resp.Code != http.StatusNotFound {
		t.Errorf("the old URL: %d", resp.Code)
	}

	resp := a.Get(strings.TrimPrefix(feed.Path, "/api/v1/calendar") + "?lang=zh-CN")
	ics := resp.Body.String()
	if resp.Code != http.StatusOK || !strings.HasPrefix(resp.Header().Get("Content-Type"), "text/calendar") {
		t.Fatalf("feed = %d %s", resp.Code, resp.Header().Get("Content-Type"))
	}
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n", "END:VCALENDAR\r\n",
		// The end is a day of the panel's zone: 04:00 on 1 January there.
		"DTSTART;VALUE=DATE:20270101\r\n", "DTEND;VALUE=DATE:20270102\r\n",
		"TRIGGER:-P7D\r\n", "DTSTART:20261201T180000Z\r\n",
		"URL:https://certs.example.com/certificates/3\r\n",
		"UID:jec-3-end-1798747200\r\n",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("no %q in:\n%s", want, ics)
		}
	}
	// No line longer than 75 bytes; commas in text escaped; the words are
	// those of the language asked for.
	for _, l := range strings.Split(ics, "\r\n") {
		if len(l) > 75 {
			t.Errorf("a line of %d bytes: %q", len(l), l)
		}
	}
	unfolded := strings.ReplaceAll(ics, "\r\n ", "")
	if !strings.Contains(unfolded, `example.com\, *.example.com\, sub.`) {
		t.Errorf("commas:\n%s", unfolded)
	}
	ends, _ := m.cat.Format(language.MustParse("zh-CN"), i18n.CalendarEventEnds, i18n.Args{"domain": "example.com"})
	if ends == "" || !strings.Contains(unfolded, "SUMMARY:"+ends) {
		t.Errorf("no %q in:\n%s", ends, unfolded)
	}
	if strings.Contains(unfolded, "new.example.com") {
		t.Error("a certificate without a version or a renewal has events")
	}

	a.Put("/feed", map[string]any{"enabled": false})
	if get(); feed.Enabled {
		t.Error("still on")
	}
}

package system

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/zone"
)

func TestUpdateCheck(t *testing.T) {
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		_, _ = w.Write([]byte(`[{"tag_name": "v0.1.0-alpha.30", "html_url": "https://example.com/a30", "prerelease": true, "published_at": "2026-10-21T00:00:00Z"}]`))
	}))
	defer srv.Close()
	old := releasesURL
	releasesURL = srv.URL
	t.Cleanup(func() { releasesURL = old })
	bus := events.New(st.DB(), slog.New(slog.DiscardHandler), func(string) bool { return true })
	bus.Seal()
	tz, err := zone.Open(t.Context(), st.DB(), nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	o := Options{DB: st.DB(), Version: "0.1.0-alpha.26", Events: bus.Scope("kernel"), Zone: tz, beats: &beats{}}
	told := func() []contracts.ReleaseEvent {
		rows, err := st.DB().QueryContext(t.Context(), "SELECT data FROM kernel_events WHERE type = ?", contracts.ReleaseAvailable)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []contracts.ReleaseEvent
		for rows.Next() {
			var data string
			var e contracts.ReleaseEvent
			if err := rows.Scan(&data); err != nil || json.Unmarshal([]byte(data), &e) != nil {
				t.Fatal(err)
			}
			out = append(out, e)
		}
		return out
	}

	// Off, as it starts: nobody is asked.
	if err := o.checkUpdates(t.Context(), srv.Client()); err != nil || asked != 0 {
		t.Fatalf("off: %v, asked %d", err, asked)
	}
	_, a := humatest.New(t)
	registerUpdates(a, o, srv.Client(), []string{"system"})
	resp := a.Put("/system/update-check", map[string]any{"enabled": true})
	var body RuntimeBody
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &body) != nil || !body.UpdateCheck {
		t.Fatalf("switch on = %d %s", resp.Code, resp.Body)
	}
	// On: a newer release is told of, once.
	for range 2 {
		if err := o.checkUpdates(t.Context(), srv.Client()); err != nil {
			t.Fatal(err)
		}
	}
	if got := told(); asked != 2 || len(got) != 1 || got[0].Version != "0.1.0-alpha.30" || got[0].Current != "0.1.0-alpha.26" || got[0].URL != "https://example.com/a30" {
		t.Fatalf("told %+v, asked %d", got, asked)
	}
}

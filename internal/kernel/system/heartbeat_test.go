package system

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/zone"
)

func TestHeartbeat(t *testing.T) {
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	asked := 0
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		w.WriteHeader(status)
	}))
	defer srv.Close()
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	tz, err := zone.Open(t.Context(), st.DB(), time.UTC, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	o := Options{DB: st.DB(), Now: func() time.Time { return now }, beats: &beats{}, Zone: tz}

	// None set: nothing asked.
	if err := o.beat(t.Context(), srv.Client()); err != nil || asked != 0 {
		t.Fatalf("without an address: %v, %d", err, asked)
	}
	_, a := humatest.New(t)
	registerHeartbeat(a, o, srv.Client(), []string{"system"})
	if resp := a.Put("/system/heartbeat", map[string]any{"url": "ftp://example.com"}); resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("an ftp address: %d", resp.Code)
	}
	if resp := a.Put("/system/heartbeat", map[string]any{"url": srv.URL + "/ping/abc"}); resp.Code != http.StatusOK ||
		!strings.Contains(resp.Body.String(), `"heartbeat":{"url":"`+srv.URL+`/ping/abc"`) {
		t.Fatalf("set: %d %s", resp.Code, resp.Body)
	}
	if err := o.beat(t.Context(), srv.Client()); err != nil || asked != 1 {
		t.Errorf("a beat: %v, %d", err, asked)
	}
	if b := o.heartbeatState(t.Context()); b.Last == nil || !b.Last.Equal(now) || b.Error != "" {
		t.Errorf("state %+v", b)
	}
	// An address that answers an error fails the job, and says so.
	status = http.StatusNotFound
	if err := o.beat(t.Context(), srv.Client()); err == nil {
		t.Error("an error answered no error")
	}
	if b := o.heartbeatState(t.Context()); b.Error != "HTTP 404" {
		t.Errorf("state %+v", b)
	}
}

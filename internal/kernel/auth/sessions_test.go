package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
)

// told returns the events of type typ published, oldest first.
func (f *fixture) told(typ string) []contracts.SignInEvent {
	f.t.Helper()
	rows, err := f.svc.db.QueryContext(f.t.Context(), "SELECT data FROM kernel_events WHERE type = ? ORDER BY id", typ)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []contracts.SignInEvent
	for rows.Next() {
		var data string
		var e contracts.SignInEvent
		if err := rows.Scan(&data); err != nil || json.Unmarshal([]byte(data), &e) != nil {
			f.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (f *fixture) sessionList(cookie string) []SessionBody {
	f.t.Helper()
	r := f.call(http.MethodGet, "/account/sessions", cookie, "")
	var body struct {
		Sessions []SessionBody `json:"sessions"`
	}
	if r.status != http.StatusOK || json.Unmarshal([]byte(r.body), &body) != nil {
		f.t.Fatalf("sessions = %d %s", r.status, r.body)
	}
	return body.Sessions
}

func TestSessions(t *testing.T) {
	f := newFixture(t, Options{CookiePath: "/"})
	bus := events.New(f.svc.db, slog.New(slog.DiscardHandler), func(string) bool { return true })
	bus.Seal()
	f.svc.opts.Events = bus.Scope("kernel")

	first := f.call(http.MethodPost, "/setup", "", f.setupBody(f.token)).cookie
	f.clock = f.clock.Add(time.Hour)
	r := f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "correct horse"}`)
	if r.cookie == nil {
		t.Fatalf("sign in = %d %s", r.status, r.body)
	}
	here := r.cookie.Value

	// This one first, then the latest; where each came from.
	list := f.sessionList(here)
	if len(list) != 2 || !list[0].Current || list[0].Via != viaPassword || list[1].Current || list[1].Via != viaSetup ||
		list[0].Client != "127.0.0.1" || list[0].Device != "Go-http-client/1.1" || !list[0].CreatedAt.Equal(f.clock) || list[0].ID == list[1].ID {
		t.Fatalf("sessions %+v", list)
	}
	if told := f.told(contracts.AccountSignedIn); len(told) != 2 || told[0].Via != viaSetup || told[1].Via != viaPassword ||
		told[1].Username != "admin" || told[1].Client != "127.0.0.1" || told[1].Device != "Go-http-client/1.1" {
		t.Fatalf("sign-ins told %+v", told)
	}

	// Its use is noted, every few minutes at most.
	f.clock = f.clock.Add(seenEvery)
	f.call(http.MethodGet, "/whoami", first.Value, "")
	if list := f.sessionList(here); list[1].LastSeen == nil || !list[1].LastSeen.Equal(f.clock) {
		t.Fatalf("last seen %+v", list[1])
	}

	// One signed out from here; not this one, which signs out otherwise.
	if r := f.call(http.MethodDelete, "/account/sessions/"+list[0].ID, here, ""); r.status != http.StatusConflict || code(t, r.body) != i18n.KernelErrorSessionCurrent {
		t.Errorf("sign out this one = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodDelete, "/account/sessions/0000", here, ""); r.status != http.StatusNotFound {
		t.Errorf("sign out an unknown one = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodDelete, "/account/sessions/"+list[1].ID, here, ""); r.status != http.StatusNoContent {
		t.Fatalf("sign out the first = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodGet, "/whoami", first.Value, ""); r.status != http.StatusUnauthorized {
		t.Errorf("the first still signed in: %d", r.status)
	}

	// Everywhere else.
	other := f.call(http.MethodPost, "/session", "", `{"username": "admin", "password": "correct horse"}`).cookie
	if r := f.call(http.MethodPost, "/account/sessions/sign-out-others", here, ""); r.status != http.StatusNoContent {
		t.Fatalf("sign out the others = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodGet, "/whoami", other.Value, ""); r.status != http.StatusUnauthorized {
		t.Errorf("another still signed in: %d", r.status)
	}
	if list := f.sessionList(here); len(list) != 1 || !list[0].Current {
		t.Errorf("left %+v", list)
	}

	// A lockout is told, once.
	for range maxFailures + 1 {
		f.call(http.MethodPost, "/session", "", `{"username": "root", "password": "guess"}`)
	}
	if told := f.told(contracts.AccountLocked); len(told) != 1 || told[0].Username != "root" || told[0].Client != "127.0.0.1" ||
		told[0].Minutes != int(lockout.Minutes()) {
		t.Fatalf("lockouts told %+v", told)
	}
}

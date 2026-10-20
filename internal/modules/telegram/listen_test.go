package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// botAPI plays the Bot API for a listener: it hands out the updates a test
// queues, a poll at a time, and records what the bot sends.
type botAPI struct {
	t       *testing.T
	mu      sync.Mutex
	queue   []update
	next    int64
	calls   []botCall
	changed chan struct{}
}

type botCall struct {
	method string
	params map[string]any
}

func newBotAPI(t *testing.T) (*botAPI, *httptest.Server) {
	a := &botAPI{t: t, next: 1, changed: make(chan struct{}, 100)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/bot"+token+"/")
		var params map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &params)
		if method == "getUpdates" && params["offset"] == float64(-1) {
			// The listener lets go of what came before it listened.
			method = "skip"
		} else if method == "getUpdates" {
			_, _ = io.WriteString(w, `{"ok":true,"result":`+a.take(params)+`}`)
			return
		}
		a.mu.Lock()
		a.calls = append(a.calls, botCall{method, params})
		a.mu.Unlock()
		a.changed <- struct{}{}
		if method == "skip" {
			_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	t.Cleanup(srv.Close)
	return a, srv
}

// take answers a poll: the updates queued, or none after a moment.
func (a *botAPI) take(params map[string]any) string {
	for range 20 {
		a.mu.Lock()
		if len(a.queue) > 0 {
			out, _ := json.Marshal(a.queue)
			a.queue = nil
			a.mu.Unlock()
			return string(out)
		}
		a.mu.Unlock()
		if params["timeout"] == float64(0) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "[]"
}

func (a *botAPI) push(u update) {
	a.mu.Lock()
	u.ID = a.next
	a.next++
	a.queue = append(a.queue, u)
	a.mu.Unlock()
}

// wait returns the next call of method the bot makes.
func (a *botAPI) wait(method string) map[string]any {
	a.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		a.mu.Lock()
		for i, c := range a.calls {
			if c.method == method {
				a.calls = append(a.calls[:i], a.calls[i+1:]...)
				a.mu.Unlock()
				return c.params
			}
		}
		a.mu.Unlock()
		select {
		case <-a.changed:
		case <-deadline:
			a.t.Fatalf("the bot did not call %s", method)
		}
	}
}

// jecAPI plays JEC's own API for the menu, and records what it is asked
// to do.
type jecAPI struct {
	mu   sync.Mutex
	done []string
}

func (j *jecAPI) call(_ context.Context, via, method, path string, _, out any) error {
	if via != "Team bot" {
		return &module.CallError{Status: http.StatusUnauthorized}
	}
	if method == http.MethodPost || method == http.MethodPatch {
		j.mu.Lock()
		j.done = append(j.done, method+" "+path)
		j.mu.Unlock()
		return nil
	}
	expires := time.Date(2027, 5, 5, 12, 0, 0, 0, time.UTC)
	renew := time.Date(2027, 4, 5, 18, 0, 0, 0, time.UTC)
	cert := certificate{
		ID: 1, Domains: []string{"example.com", "www.example.com"}, State: "ok", Source: "provider", SourceProvider: 10, SolverProvider: 11,
		RenewAt: &renew, Current: &version{ID: 5, NotAfter: expires, Issuer: "Test CA"}, Targets: []int64{12},
		Deployments: []deployment{{Target: 12, VersionID: 5, State: "done", FinishedAt: &renew}},
	}
	failed := certificate{
		ID: 2, Domains: []string{"broken.example"}, State: "failed", Source: "provider", SourceProvider: 10,
		Issuance: &issuance{ID: 9, State: "failed", Reason: "first", Error: &apiMessage{Key: i18n.KernelErrorInternal}},
	}
	var body any
	switch path {
	case "/certs/certificates":
		body = map[string]any{"certificates": []certificate{failed, cert}}
	case "/certs/certificates/1":
		body = map[string]any{"certificate": cert}
	case "/certs/certificates/2":
		body = map[string]any{"certificate": failed}
	case "/providers":
		body = map[string]any{"providers": []instance{{ID: 10, Name: "Let's Encrypt", Kind: "source"}, {ID: 11, Name: "DNS", Kind: "solver"}, {ID: 12, Name: "<CDN>", Kind: "target"}}}
	case "/monitor/certificates/1":
		body = checks{Tracked: true, Results: []check{{Domain: "example.com", State: "ok"}, {Domain: "www.example.com", State: "ok"}}}
	case "/monitor/states":
		body = map[string]any{"certificates": []tally{{CertificateID: 1, Old: 1}}}
	case "/certs/runs?days=7":
		body = map[string]any{"runs": []run{{Certificate: 1, Domain: "example.com", Action: "deploy", Trigger: "auto", State: "done", Target: 12, CreatedAt: renew}}}
	case "/manifest":
		body = manifest{Version: "1.2.3", Platform: "linux/amd64", TimeZone: "Asia/Shanghai"}
	case "/watch/sites":
		end := renew.Add(-10 * 24 * time.Hour)
		body = map[string]any{"sites": []site{
			{Host: "ok.example.org", Port: 443, State: "ok", NotAfter: &end},
			{Host: "partner.example.org", Port: 8443, Name: "Partner", State: "invalid", NotAfter: &end},
		}}
	case "/watch/domains":
		soon, later := renew.Add(-20*24*time.Hour), renew.Add(300*24*time.Hour)
		body = map[string]any{"domains": []domain{
			{Name: "fine.org", State: "ok", ExpiresAt: &later},
			{Name: "shop.de", State: "unknown"},
			{Name: "ending.org", State: "expiring", ExpiresAt: &soon},
		}}
	case "/watch/transparency":
		body = map[string]any{"enabled": true, "certificates": []map[string]any{
			{"names": []string{"www.fine.org"}, "issuer": "Some <CA>", "state": "unknown"},
			{"names": []string{"cdn.fine.org"}, "issuer": "Sectigo", "state": "baseline"},
		}}
	default:
		return &module.CallError{Status: http.StatusNotFound, Code: i18n.KernelErrorNotFound}
	}
	data, _ := json.Marshal(body)
	return json.Unmarshal(data, out)
}

func (j *jecAPI) asked() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.done
	j.done = nil
	return out
}

// listening starts a listener of a bot of chat -1001234, in English.
func listening(t *testing.T, api string, actions bool) (*jecAPI, func()) {
	t.Helper()
	cat, err := i18n.Default()
	if err != nil {
		t.Fatal(err)
	}
	j := &jecAPI{}
	m := New()
	m.call, m.cat, m.log, m.wait = j.call, cat, slog.New(slog.DiscardHandler), time.Second
	m.zone = func() *time.Location { return time.UTC }
	m.baseURL = "https://jec.example"
	m.now = func() time.Time { return time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC) }
	cfg := map[string]string{"commands": "true", "locale": "en-US"}
	if actions {
		cfg["actions"] = "true"
	}
	b := open(t, api, cfg)
	b.name = "Team bot"
	l := m.listen(t.Context(), b, time.Time{})
	return j, l.stop
}

func text(t *testing.T, params map[string]any) string {
	t.Helper()
	s, _ := params["text"].(string)
	return s
}

// buttons are the data of the buttons a message carries, in order.
func buttons(params map[string]any) []string {
	var out []string
	markup, _ := params["reply_markup"].(map[string]any)
	rows, _ := markup["inline_keyboard"].([]any)
	for _, row := range rows {
		for _, b := range row.([]any) {
			b := b.(map[string]any)
			if d, ok := b["callback_data"].(string); ok {
				out = append(out, d)
			} else {
				out = append(out, "url:"+b["url"].(string))
			}
		}
	}
	return out
}

func TestMenu(t *testing.T) {
	a, srv := newBotAPI(t)
	j, stop := listening(t, srv.URL, true)
	defer stop()
	ours := chat{ID: -1001234}

	// The commands, in the bot's language, for its chat.
	cmds := a.wait("setMyCommands")
	a.wait("skip")
	if list, _ := cmds["commands"].([]any); len(list) != 7 || list[0].(map[string]any)["description"] != "Open the menu" {
		t.Errorf("commands %v", cmds)
	}
	if scope, _ := cmds["scope"].(map[string]any); scope["chat_id"] != float64(-1001234) {
		t.Errorf("commands for %v", cmds["scope"])
	}

	// The main menu: how the certificates are, and where to go.
	a.push(update{Message: &message{ID: 1, Chat: ours, Text: "/menu@JecBot"}})
	main := a.wait("sendMessage")
	if s := text(t, main); !strings.Contains(s, "2 certificates") || !strings.Contains(s, "OK 1 · Expiring 0 · Need attention 1") ||
		!strings.Contains(s, "Next renewal: 2027-04-05 18:00 · example.com") {
		t.Errorf("menu %q", s)
	}
	if got := strings.Join(buttons(main), " "); got != "l:0 t r o w s h m url:https://jec.example" {
		t.Errorf("menu buttons %s", got)
	}

	// The watched sites, those to look at first.
	a.push(update{Message: &message{ID: 2, Chat: ours, Text: "/sites"}})
	if s := text(t, a.wait("sendMessage")); !strings.Contains(s, "Expiry watch") ||
		strings.Index(s, "Partner (partner.example.org:8443)") > strings.Index(s, "ok.example.org") || !strings.Contains(s, "Not trusted") ||
		!strings.Contains(s, "Domain registrations") || strings.Index(s, "ending.org") > strings.Index(s, "fine.org") ||
		strings.Index(s, "fine.org") > strings.Index(s, "shop.de") || !strings.Contains(s, "ending.org</b> · Ends soon") ||
		!strings.Contains(s, "<b>Certificate transparency</b>") || !strings.Contains(s, "www.fine.org</b> · Some &lt;CA&gt;") || strings.Contains(s, "cdn.fine.org") {
		t.Errorf("sites %q", s)
	}

	// A certificate: how it is, where it goes, what it serves, what it can do.
	a.push(update{Callback: &callback{ID: "q1", Data: "c:1", Message: &message{ID: 7, Chat: ours}}})
	a.wait("answerCallbackQuery")
	cert := a.wait("editMessageText")
	if cert["message_id"] != float64(7) {
		t.Errorf("edited %v", cert["message_id"])
	}
	s := text(t, cert)
	for _, want := range []string{
		"<b>example.com</b>", "Also www.example.com", "OK · 197 days left, until 2027-05-05", "Renews on its own: 2027-04-05 18:00",
		"Source: Let&#39;s Encrypt · Validation: DNS", "Issuer: Test CA", "&lt;CDN&gt; · Deployed · 2027-04-05 18:00",
		"example.com, www.example.com · The current certificate",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("certificate %q lacks %q", s, want)
		}
	}
	if got := strings.Join(buttons(cert), " "); got != "ar:1 ad:1 xc:1 xp:1 c:1 l:0 m url:https://jec.example/certificates/1" {
		t.Errorf("certificate buttons %s", got)
	}

	// The button under a note shows the certificate in a message of its
	// own, which leaves the note as it was.
	a.push(update{Callback: &callback{ID: "q9", Data: "n:c:1", Message: &message{ID: 3, Chat: ours}}})
	a.wait("answerCallbackQuery")
	if fresh := a.wait("sendMessage"); !strings.Contains(text(t, fresh), "<b>example.com</b>") {
		t.Errorf("from a note %q", text(t, fresh))
	}

	// Renewing: asked first, then done, by the bot's name.
	a.push(update{Callback: &callback{ID: "q2", Data: "ar:1", Message: &message{ID: 7, Chat: ours}}})
	a.wait("answerCallbackQuery")
	ask := a.wait("editMessageText")
	if s := text(t, ask); !strings.Contains(s, "Renew example.com now?") || !strings.Contains(s, "asks Let&#39;s Encrypt") {
		t.Errorf("ask %q", s)
	}
	if got := strings.Join(buttons(ask), " "); got != "xr:1 c:1" {
		t.Errorf("ask buttons %s", got)
	}
	if done := j.asked(); len(done) != 0 {
		t.Errorf("did %v before the confirmation", done)
	}
	a.push(update{Callback: &callback{ID: "q3", Data: "xr:1", Message: &message{ID: 7, Chat: ours}}})
	if toast := a.wait("answerCallbackQuery"); !strings.Contains(toast["text"].(string), "renewal started") {
		t.Errorf("toast %v", toast)
	}
	if s := text(t, a.wait("editMessageText")); !strings.Contains(s, "renewal started") {
		t.Errorf("after renewing %q", s)
	}
	if done := j.asked(); len(done) != 1 || done[0] != "POST /certs/certificates/1/issue" {
		t.Errorf("did %v", done)
	}
	// Its renewal paused at once: nothing to confirm, as it can be resumed.
	a.push(update{Callback: &callback{ID: "q5", Data: "xp:1", Message: &message{ID: 7, Chat: ours}}})
	if toast := a.wait("answerCallbackQuery"); !strings.Contains(toast["text"].(string), "Renewal paused") {
		t.Errorf("toast %v", toast)
	}
	a.wait("editMessageText")
	if done := j.asked(); len(done) != 1 || done[0] != "PATCH /certs/certificates/1" {
		t.Errorf("did %v", done)
	}

	// The other views.
	for _, v := range []struct{ data, want string }{
		{"t", "broken.example</b> · Issuance failed"},
		{"r", "Deployment <b>example.com</b> → &lt;CDN&gt; · done"},
		{"o", "1 domain still serves an older certificate"},
		{"s", "Version: 1.2.3"},
		{"l:0", "197 days left"},
		{"c:3", "JEC could not do it"},
	} {
		a.push(update{Callback: &callback{ID: "q", Data: v.data, Message: &message{ID: 7, Chat: ours}}})
		a.wait("answerCallbackQuery")
		if s := text(t, a.wait("editMessageText")); !strings.Contains(s, v.want) {
			t.Errorf("view %s: %q lacks %q", v.data, s, v.want)
		}
	}

	// Another chat learns its ID, and nothing more.
	a.push(update{Message: &message{ID: 2, Chat: chat{ID: 42}, Text: "/start"}})
	if s := text(t, a.wait("sendMessage")); !strings.Contains(s, "42") || strings.Contains(s, "example.com") {
		t.Errorf("another chat got %q", s)
	}
	a.push(update{Callback: &callback{ID: "q4", Data: "xr:1", Message: &message{ID: 3, Chat: chat{ID: 42}}}})
	a.wait("answerCallbackQuery")
	if done := j.asked(); len(done) != 0 {
		t.Errorf("another chat did %v", done)
	}
}

func TestMenuWithoutActions(t *testing.T) {
	a, srv := newBotAPI(t)
	j, stop := listening(t, srv.URL, false)
	defer stop()
	a.wait("skip")
	ours := chat{ID: -1001234}
	a.push(update{Callback: &callback{ID: "q1", Data: "c:1", Message: &message{ID: 7, Chat: ours}}})
	a.wait("answerCallbackQuery")
	if got := strings.Join(buttons(a.wait("editMessageText")), " "); got != "c:1 l:0 m url:https://jec.example/certificates/1" {
		t.Errorf("buttons without actions %s", got)
	}
	a.push(update{Callback: &callback{ID: "q2", Data: "xr:1", Message: &message{ID: 7, Chat: ours}}})
	a.wait("answerCallbackQuery")
	if s := text(t, a.wait("editMessageText")); !strings.Contains(s, "does not allow actions") {
		t.Errorf("an action without leave: %q", s)
	}
	if done := j.asked(); len(done) != 0 {
		t.Errorf("did %v", done)
	}
}

func TestCommand(t *testing.T) {
	for in, want := range map[string]string{"/menu": "menu", "/Certs@JecBot now": "certs", "/": "", "/help\nmore": "help"} {
		if got := command(in); got != want {
			t.Errorf("command(%q) = %q, want %q", in, got, want)
		}
	}
}

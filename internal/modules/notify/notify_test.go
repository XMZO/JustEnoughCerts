package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

var start = time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)

type fixture struct {
	t   *testing.T
	api humatest.TestAPI
	reg *providers.Registry
	svc *service

	mu    sync.Mutex
	got   map[int64][]contracts.Note // the notes each notifier sent
	fails map[int64]error            // what each notifier fails with
}

// fakeNotifier sends to the fixture.
type fakeNotifier struct {
	f  *fixture
	id int64
}

func (n *fakeNotifier) Notify(_ context.Context, note contracts.Note) error {
	n.f.mu.Lock()
	defer n.f.mu.Unlock()
	if err := n.f.fails[n.id]; err != nil {
		return err
	}
	n.f.got[n.id] = append(n.f.got[n.id], note)
	return nil
}

func (*fakeNotifier) Close() error { return nil }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(t.Context(), "notify", New().Migrations()); err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, got: map[int64][]contracts.Note{}, fails: map[int64]error{}}
	f.reg = providers.New(st.DB(), box.Scope("kernel"), func(string) bool { return true })
	fake := f.reg.Scope("fake")
	err = fake.Add(providers.Type{ID: "fake.chat", Kind: providers.Notifier, Brand: "Chat", Fields: contracts.NotifierFields()},
		func(_ context.Context, in providers.Instance) (any, error) {
			return &fakeNotifier{f: f, id: in.ID}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	err = fake.Add(providers.Type{ID: "fake.cdn", Kind: providers.Target, Brand: "CDN"},
		func(context.Context, providers.Instance) (any, error) { return nil, errors.New("not used") })
	if err != nil {
		t.Fatal(err)
	}
	f.reg.Seal()
	cat, err := i18n.Default()
	if err != nil {
		t.Fatal(err)
	}
	f.svc = &service{db: st.DB(), prov: f.reg.Scope("notify"), log: slog.New(slog.DiscardHandler), cat: cat,
		base: "https://jec.example.com", zone: time.UTC, now: func() time.Time { return start }}
	_, f.api = humatest.New(t)
	registerAPI(f.api, f.svc)
	return f
}

// notifier adds a fake notifier with config.
func (f *fixture) notifier(name string, config map[string]string) int64 {
	f.t.Helper()
	id, err := f.reg.Create(f.t.Context(), "fake.chat", name, config, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) notes(id int64) []contracts.Note {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.Note{}, f.got[id]...)
}

// deliver hands the event to the module as the bus would.
func (f *fixture) deliver(id int64, typ string, data contracts.CertificateEvent) error {
	f.t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		f.t.Fatal(err)
	}
	return f.svc.handle(f.t.Context(), events.Event{ID: id, Type: typ, Source: "certs", Subject: strconv.FormatInt(data.CertificateID, 10), Data: raw, Time: start})
}

func TestNotes(t *testing.T) {
	f := newFixture(t)
	en := f.notifier("ops", map[string]string{"locale": "en-US"})
	zh := f.notifier("team", map[string]string{"locale": "zh-CN", "on_issued": "true", "on_issue_failed": "false"})

	failed := contracts.CertificateEvent{CertificateID: 3, Domains: []string{"example.com", "www.example.com"},
		Issuance: &contracts.Issuance{ID: 9, Attempt: 2, Error: contracts.Message{Key: i18n.CertsErrorIssueFailed}, Retry: start.Add(20 * time.Minute)}}
	if err := f.deliver(1, contracts.CertificateIssueFailed, failed); err != nil {
		t.Fatal(err)
	}
	notes := f.notes(en)
	if len(notes) != 1 || len(f.notes(zh)) != 0 {
		t.Fatalf("notes: en %v, zh %v", notes, f.notes(zh))
	}
	n := notes[0]
	issueFailed, _ := f.svc.cat.Format(language.AmericanEnglish, i18n.CertsErrorIssueFailed, nil)
	want := issueFailed + "\nJEC tries again at 2026-10-17 08:20 UTC.\nThe certificate also covers www.example.com.\n" +
		"Details: https://jec.example.com/certificates/3/issuances/9"
	if n.Event != contracts.CertificateIssueFailed || n.Title != "Could not issue the certificate for example.com" || n.Text != want ||
		n.Link != "https://jec.example.com/certificates/3/issuances/9" || !n.Time.Equal(start) || !strings.Contains(string(n.Data), `"issuance"`) {
		t.Errorf("note %+v", n)
	}

	// The second notifier wants new certificates, in its own language.
	issued := contracts.CertificateEvent{CertificateID: 3, Domains: []string{"example.com"}, NotAfter: start.Add(90 * 24 * time.Hour)}
	if err := f.deliver(2, contracts.CertificateIssued, issued); err != nil {
		t.Fatal(err)
	}
	zhTitle, _ := f.svc.cat.Format(language.MustParse("zh-CN"), i18n.NotifyNoteIssuedTitle, i18n.Args{"domain": "example.com"})
	if got := f.notes(zh); len(got) != 1 || got[0].Title != zhTitle || !strings.Contains(got[0].Text, "2027-01-15") || len(f.notes(en)) != 1 {
		t.Errorf("zh notes %+v", got)
	}

	// The same event delivered again sends nothing again.
	if err := f.deliver(1, contracts.CertificateIssueFailed, failed); err != nil || len(f.notes(en)) != 1 {
		t.Errorf("again: %v, %d notes", err, len(f.notes(en)))
	}
}

func TestDeployAndExpiryNotes(t *testing.T) {
	f := newFixture(t)
	en := f.notifier("ops", map[string]string{"locale": "en-US", "on_deployed": "true"})
	cdn, err := f.reg.Create(t.Context(), "fake.cdn", "edge", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		typ   string
		data  contracts.CertificateEvent
		title string
		text  string
	}{
		{contracts.CertificateDeployFailed, contracts.CertificateEvent{CertificateID: 3, Domains: []string{"example.com"},
			Deployment: &contracts.Deployment{ID: 4, Target: cdn, Attempt: 8, Error: contracts.Message{Key: i18n.CertsErrorDeployFailed}}},
			"Could not deploy example.com to edge", "JEC will not try again by itself: this needs you."},
		{contracts.CertificateDeployed, contracts.CertificateEvent{CertificateID: 3, Domains: []string{"example.com"},
			Deployment: &contracts.Deployment{ID: 5, Target: cdn + 100, Attempt: 1}},
			"example.com is deployed to a deleted target", "Details: https://jec.example.com/certificates/3/deployments/5"},
		{contracts.CertificateExpiring, contracts.CertificateEvent{CertificateID: 3, Domains: []string{"example.com"},
			NotAfter: start.Add(7*24*time.Hour + time.Hour), Expiry: contracts.ExpiryUrgent},
			"The certificate for example.com expires in 7 days", "It ends on 2026-10-24."},
		{contracts.CertificateExpiring, contracts.CertificateEvent{CertificateID: 3, Domains: []string{"example.com"},
			NotAfter: start.Add(time.Hour), Expiry: contracts.ExpiryUrgent},
			"The certificate for example.com expires today", "It ends on 2026-10-17."},
		{contracts.CertificateExpiring, contracts.CertificateEvent{CertificateID: 3, Domains: []string{"example.com"},
			NotAfter: start.Add(-time.Hour), Expiry: contracts.ExpiryExpired},
			"The certificate for example.com has expired", "It ended on 2026-10-17."},
	}
	for i, c := range cases {
		if err := f.deliver(int64(i+1), c.typ, c.data); err != nil {
			t.Fatal(err)
		}
		notes := f.notes(en)
		if len(notes) != i+1 {
			t.Fatalf("%s: %d notes", c.typ, len(notes))
		}
		if n := notes[i]; n.Title != c.title || !strings.Contains(n.Text, c.text) {
			t.Errorf("%s: %q\n%s", c.typ, n.Title, n.Text)
		}
	}
}

func TestRetries(t *testing.T) {
	f := newFixture(t)
	flaky := f.notifier("flaky", map[string]string{"locale": "en-US"})
	broken := f.notifier("broken", map[string]string{"locale": "en-US"})
	fine := f.notifier("fine", map[string]string{"locale": "en-US"})
	f.fails[flaky] = &contracts.Problem{Key: i18n.NotifyErrorSend, Err: errors.New("connection reset")}
	f.fails[broken] = retry.Permanent(&contracts.Problem{Key: i18n.NotifyErrorUnknown})

	data := contracts.CertificateEvent{CertificateID: 3, Domains: []string{"example.com"}, NotAfter: start, Expiry: contracts.ExpiryExpired}
	if err := f.deliver(1, contracts.CertificateExpiring, data); err == nil || retry.IsPermanent(err) {
		t.Fatalf("a notifier that may work later: %v", err)
	}
	f.fails[flaky] = nil
	if err := f.deliver(1, contracts.CertificateExpiring, data); err != nil {
		t.Fatalf("the second delivery: %v", err)
	}
	if len(f.notes(flaky)) != 1 || len(f.notes(fine)) != 1 || len(f.notes(broken)) != 0 {
		t.Errorf("notes: flaky %d, fine %d, broken %d", len(f.notes(flaky)), len(f.notes(fine)), len(f.notes(broken)))
	}

	sends := f.sends()
	if s := sends[flaky]; s.State != "sent" || s.Attempts != 2 || s.Error != nil {
		t.Errorf("flaky %+v", s)
	}
	if s := sends[broken]; s.State != "failed" || s.Attempts != 1 || s.Error == nil || s.Error.Key != i18n.NotifyErrorUnknown {
		t.Errorf("broken %+v", s)
	}
}

func (f *fixture) sends() map[int64]SendBody {
	f.t.Helper()
	resp := f.api.Get("/sends")
	if resp.Code != http.StatusOK {
		f.t.Fatalf("GET /sends: %d %s", resp.Code, resp.Body)
	}
	var out struct {
		Sends []SendBody `json:"sends"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	m := map[int64]SendBody{}
	for _, s := range out.Sends {
		m[s.Notifier] = s
	}
	return m
}

func TestTestAPI(t *testing.T) {
	f := newFixture(t)
	ok := f.notifier("ok", map[string]string{"locale": "zh-CN", "on_issue_failed": "false"})
	bad := f.notifier("bad", map[string]string{"locale": "en-US"})
	f.fails[bad] = retry.Permanent(&contracts.Problem{Key: i18n.NotifyErrorUnknown, Params: map[string]any{"code": 401}})

	if resp := f.api.Post("/notifiers/" + strconv.FormatInt(ok, 10) + "/test"); resp.Code != http.StatusNoContent {
		t.Fatalf("test: %d %s", resp.Code, resp.Body)
	}
	title, _ := f.svc.cat.Format(language.MustParse("zh-CN"), i18n.NotifyTestTitle, nil)
	if got := f.notes(ok); len(got) != 1 || got[0].Event != contracts.NoteTest || got[0].Title != title || got[0].Link != "" {
		t.Errorf("test notes %+v", got)
	}

	resp := f.api.Post("/notifiers/" + strconv.FormatInt(bad, 10) + "/test")
	var e struct {
		Code   string         `json:"code"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &e); err != nil || resp.Code != http.StatusBadGateway || e.Code != string(i18n.NotifyErrorUnknown) || e.Params["code"] != float64(401) {
		t.Errorf("a failing test: %d %s", resp.Code, resp.Body)
	}
	if resp := f.api.Post("/notifiers/999/test"); resp.Code != http.StatusNotFound {
		t.Errorf("no such notifier: %d", resp.Code)
	}

	sends := f.sends()
	if s := sends[ok]; s.Event != contracts.NoteTest || s.State != "sent" {
		t.Errorf("sends %+v", sends)
	}
	if s := sends[bad]; s.State != "failed" || s.Error == nil {
		t.Errorf("sends %+v", sends)
	}
}

func TestWants(t *testing.T) {
	for _, c := range []struct {
		config map[string]string
		typ    string
		want   bool
	}{
		{map[string]string{}, contracts.CertificateIssueFailed, true},
		{map[string]string{}, contracts.CertificateIssued, false},
		{map[string]string{"on_issued": "true"}, contracts.CertificateIssued, true},
		{map[string]string{"on_expiring": "false"}, contracts.CertificateExpiring, false},
		{map[string]string{}, contracts.CertificateDeleted, false},
	} {
		if got := wants(c.config, c.typ); got != c.want {
			t.Errorf("wants(%v, %s) = %v", c.config, c.typ, got)
		}
	}
}

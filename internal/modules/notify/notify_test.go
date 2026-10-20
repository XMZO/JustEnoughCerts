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
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
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
		base: "https://jec.example.com", zone: func() *time.Location { return time.UTC }, now: func() time.Time { return start }}
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
		n.Subject != "[JEC] Could not issue the certificate for example.com" ||
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
	subject, _ := f.svc.cat.Format(language.MustParse("zh-CN"), i18n.NotifyNoteSubject, i18n.Args{"title": title})
	if got := f.notes(ok); len(got) != 1 || got[0].Event != contracts.NoteTest || got[0].Title != title || got[0].Subject != subject || got[0].Link != "" {
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

func TestDigestNote(t *testing.T) {
	f := newFixture(t)
	weekly := f.notifier("weekly", map[string]string{"locale": "en-US", "on_digest": "true"})
	quiet := f.notifier("quiet", map[string]string{"locale": "en-US"}) // digests are off by default
	d := contracts.Digest{Total: 3, OK: 1,
		Attention: []contracts.DigestItem{{CertificateID: 2, Domain: "b.example.com", State: "deploy_failed"}},
		Renewals:  []contracts.DigestItem{{CertificateID: 1, Domain: "a.example.com", State: "ok", RenewAt: start.Add(48 * time.Hour)}},
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.handle(t.Context(), events.Event{ID: 1, Type: contracts.CertificatesDigest, Source: "certs", Subject: "2026-W43", Data: raw, Time: start}); err != nil {
		t.Fatal(err)
	}
	notes := f.notes(weekly)
	if len(notes) != 1 || len(f.notes(quiet)) != 0 {
		t.Fatalf("notes: %v, quiet %v", notes, f.notes(quiet))
	}
	n := notes[0]
	want := "Needing attention:\n· b.example.com: Deployment failed\nRenewals in the next 7 days:\n· a.example.com: 2026-10-19 08:00\n" +
		"Details: https://jec.example.com/"
	if n.Title != "This week: 3 certificates, 1 needs attention" || n.Text != want || n.Link != "https://jec.example.com/" {
		t.Errorf("note %q\n%s", n.Title, n.Text)
	}

	// All well, and nothing to renew.
	raw, _ = json.Marshal(contracts.Digest{Total: 1, OK: 1})
	if err := f.svc.handle(t.Context(), events.Event{ID: 2, Type: contracts.CertificatesDigest, Source: "certs", Subject: "2026-W44", Data: raw, Time: start}); err != nil {
		t.Fatal(err)
	}
	if n := f.notes(weekly)[1]; n.Title != "This week: 1 certificate, all well" || !strings.HasPrefix(n.Text, "No renewals in the next 7 days.") {
		t.Errorf("all well: %q\n%s", n.Title, n.Text)
	}

	// What else needs someone, as the API tells: the watch module off.
	f.svc.call = func(_ context.Context, _, _, path string, _, out any) error {
		answers := map[string]string{
			"/providers": `{"providers": [{"name": "ESA", "kind": "target", "lastTest": {"ok": false}}, {"name": "Mail", "kind": "notifier", "lastTest": {"ok": false}},
				{"name": "DNS", "kind": "solver", "lastTest": {"ok": true}}]}`,
		}
		body, ok := answers[path]
		if !ok {
			return &module.CallError{Status: http.StatusNotFound}
		}
		return json.Unmarshal([]byte(body), out)
	}
	if err := f.svc.handle(t.Context(), events.Event{ID: 3, Type: contracts.CertificatesDigest, Source: "certs", Subject: "2026-W45", Data: raw, Time: start}); err != nil {
		t.Fatal(err)
	}
	if n := f.notes(weekly)[2]; !strings.Contains(n.Text, "\nProviders failing their test: ESA\n") || strings.Contains(n.Text, "Sites") {
		t.Errorf("with more: %q\n%s", n.Title, n.Text)
	}
}

func TestSiteNote(t *testing.T) {
	f := newFixture(t)
	en := f.notifier("ops", map[string]string{"locale": "en-US"}) // site notes are on by default
	off := f.notifier("quiet", map[string]string{"locale": "en-US", "on_site_expiring": "false"})
	d := contracts.SiteEvent{SiteID: 2, Host: "partner.example.com", Port: 8443, Name: "Partner", Issuer: "R12",
		NotAfter: start.Add(5*24*time.Hour + time.Hour), Expiry: contracts.ExpiryUrgent}
	raw, _ := json.Marshal(d)
	if err := f.svc.handle(t.Context(), events.Event{ID: 1, Type: contracts.SiteExpiring, Source: "watch", Subject: "2", Data: raw, Time: start}); err != nil {
		t.Fatal(err)
	}
	notes := f.notes(en)
	if len(notes) != 1 || len(f.notes(off)) != 0 {
		t.Fatalf("notes %v, quiet %v", notes, f.notes(off))
	}
	if n := notes[0]; n.Title != "The certificate of Partner (partner.example.com:8443) ends in 5 days" ||
		!strings.HasPrefix(n.Text, "It ends on 2026-10-22; issued by R12.") || n.Link != "https://jec.example.com/sites" {
		t.Errorf("note %q\n%s\n%s", n.Title, n.Text, n.Link)
	}
}

func TestDomainNote(t *testing.T) {
	f := newFixture(t)
	en := f.notifier("ops", map[string]string{"locale": "en-US"}) // domain notes are on by default
	off := f.notifier("quiet", map[string]string{"locale": "en-US", "on_domain_expiring": "false"})
	for i, d := range []contracts.DomainEvent{
		{Domain: "example.com", ExpiresAt: start.Add(20*24*time.Hour + time.Hour), Expiry: contracts.ExpiryWindow, Registrar: "NameCheap, Inc.", Certificates: []int64{1, 4}},
		{Domain: "example.org", ExpiresAt: start.Add(-time.Hour), Expiry: contracts.ExpiryExpired, Certificates: []int64{}},
	} {
		raw, _ := json.Marshal(d)
		ev := events.Event{ID: int64(i + 1), Type: contracts.DomainExpiring, Source: "watch", Subject: d.Domain, Data: raw, Time: start}
		if err := f.svc.handle(t.Context(), ev); err != nil {
			t.Fatal(err)
		}
	}
	notes := f.notes(en)
	if len(notes) != 2 || len(f.notes(off)) != 0 {
		t.Fatalf("notes %v, quiet %v", notes, f.notes(off))
	}
	if n := notes[0]; n.Title != "The registration of example.com ends in 20 days" ||
		n.Text != "It ends on 2026-11-06 (registrar: NameCheap, Inc.); renew it there.\n2 certificates JEC manages are under this domain.\nDetails: https://jec.example.com/sites" {
		t.Errorf("note %q\n%s", n.Title, n.Text)
	}
	if n := notes[1]; n.Title != "The registration of example.org has ended" || !strings.HasPrefix(n.Text, "It ends on 2026-10-17.\nDetails:") {
		t.Errorf("note %q\n%s", n.Title, n.Text)
	}
}

func TestSpottedNote(t *testing.T) {
	f := newFixture(t)
	en := f.notifier("ops", map[string]string{"locale": "en-US"}) // on by default
	off := f.notifier("quiet", map[string]string{"locale": "en-US", "on_spotted": "false"})
	d := contracts.SpottedEvent{Domain: "example.com", Names: []string{"example.com", "www.example.com"}, Issuer: "Some CA",
		NotBefore: start, NotAfter: start.Add(90 * 24 * time.Hour), SHA256: "ab12"}
	raw, _ := json.Marshal(d)
	ev := events.Event{ID: 1, Type: contracts.CertificateSpotted, Source: "watch", Subject: "17", Data: raw, Time: start}
	if err := f.svc.handle(t.Context(), ev); err != nil {
		t.Fatal(err)
	}
	notes := f.notes(en)
	if len(notes) != 1 || len(f.notes(off)) != 0 {
		t.Fatalf("notes %v, quiet %v", notes, f.notes(off))
	}
	if n := notes[0]; n.Title != "A certificate JEC does not hold, for example.com" ||
		!strings.HasPrefix(n.Text, "Names: example.com, www.example.com\nIssued by Some CA, valid from 2026-10-17 to 2027-01-15.\n") ||
		!strings.Contains(n.Text, "The certificate: https://crt.sh/?sha256=ab12\nDetails: https://jec.example.com/sites") {
		t.Errorf("note %q\n%s", n.Title, n.Text)
	}
}

func TestAccountNotes(t *testing.T) {
	f := newFixture(t)
	en := f.notifier("ops", map[string]string{"locale": "en-US", "on_sign_in": "true"}) // sign-ins are off by default
	quiet := f.notifier("quiet", map[string]string{"locale": "en-US"})
	for i, ev := range []struct {
		typ string
		d   contracts.SignInEvent
	}{
		{contracts.AccountSignedIn, contracts.SignInEvent{Username: "admin", Client: "203.0.113.7", Device: "Chrome 141 · Windows", Via: "sso"}},
		{contracts.AccountLocked, contracts.SignInEvent{Username: "root", Client: "198.51.100.9", Minutes: 15}},
	} {
		raw, _ := json.Marshal(ev.d)
		if err := f.svc.handle(t.Context(), events.Event{ID: int64(i + 1), Type: ev.typ, Source: "kernel", Subject: ev.d.Client, Data: raw, Time: start}); err != nil {
			t.Fatal(err)
		}
	}
	notes := f.notes(en)
	if len(notes) != 2 || len(f.notes(quiet)) != 1 {
		t.Fatalf("notes %v, quiet %v", notes, f.notes(quiet))
	}
	if n := notes[0]; n.Title != "admin signed in to JEC" ||
		!strings.HasPrefix(n.Text, "By single sign-on\nFrom 203.0.113.7\nDevice: Chrome 141 · Windows\nIf it was not you") || n.Link != "https://jec.example.com/settings" {
		t.Errorf("note %q\n%s", n.Title, n.Text)
	}
	if n := notes[1]; n.Title != "Someone failed to sign in to JEC too many times: locked out for 15 minutes" ||
		!strings.HasPrefix(n.Text, "Username tried: root\nFrom 198.51.100.9\nIf it was not you") {
		t.Errorf("note %q\n%s", n.Title, n.Text)
	}
}

func TestProviderNote(t *testing.T) {
	f := newFixture(t)
	en := f.notifier("ops", map[string]string{"locale": "en-US"}) // on by default
	d := contracts.ProviderEvent{ProviderID: 7, Name: "ESA", Kind: "target", Type: "esa.site",
		Problem: contracts.Message{Key: "kernel.error.provider_try", Params: map[string]any{"message": "InvalidAccessKeyId"}}}
	raw, _ := json.Marshal(d)
	if err := f.svc.handle(t.Context(), events.Event{ID: 1, Type: contracts.ProviderFailing, Source: "kernel", Subject: "7", Data: raw, Time: start}); err != nil {
		t.Fatal(err)
	}
	notes := f.notes(en)
	if len(notes) != 1 || notes[0].Title != "ESA fails its test" || notes[0].Link != "https://jec.example.com/targets" ||
		!strings.HasPrefix(notes[0].Text, "Why: The provider did not add the record: InvalidAccessKeyId\nJEC tests") {
		t.Fatalf("notes %+v", notes)
	}
}

func TestRevokedNote(t *testing.T) {
	f := newFixture(t)
	en := f.notifier("ops", map[string]string{"locale": "en-US"}) // on by default
	for i, renewing := range []bool{true, false} {
		d := contracts.CertificateEvent{CertificateID: 3, Domains: []string{"example.com"}, VersionID: 9, NotAfter: start.Add(60 * 24 * time.Hour),
			Revocation: &contracts.Revocation{At: start.Add(-time.Hour), Reason: 1, Renewing: renewing}}
		raw, _ := json.Marshal(d)
		ev := events.Event{ID: int64(i + 1), Type: contracts.CertificateRevoked, Source: "certs", Subject: "3", Data: raw, Time: start}
		if err := f.svc.handle(t.Context(), ev); err != nil {
			t.Fatal(err)
		}
	}
	notes := f.notes(en)
	if len(notes) != 2 {
		t.Fatalf("notes %v", notes)
	}
	if n := notes[0]; n.Title != "The CA revoked the certificate of example.com" ||
		!strings.HasPrefix(n.Text, "Revoked on 2026-10-17; reason: the private key leaked.\nJEC is issuing a new one") {
		t.Errorf("note %q\n%s", n.Title, n.Text)
	}
	if n := notes[1]; !strings.Contains(n.Text, "no source for JEC") {
		t.Errorf("note %q\n%s", n.Title, n.Text)
	}
}

func TestReminderNote(t *testing.T) {
	f := newFixture(t)
	en := f.notifier("ops", map[string]string{"locale": "en-US"}) // on by default
	d := contracts.CertificateEvent{CertificateID: 3, Domains: []string{"example.com"}, VersionID: 9, NotAfter: start.Add(60 * 24 * time.Hour),
		Reminder: "Renew the PositiveSSL subscription", RemindOn: "2026-10-17"}
	raw, _ := json.Marshal(d)
	if err := f.svc.handle(t.Context(), events.Event{ID: 1, Type: contracts.CertificateReminder, Source: "certs", Subject: "3", Data: raw, Time: start}); err != nil {
		t.Fatal(err)
	}
	notes := f.notes(en)
	if len(notes) != 1 || notes[0].Title != "Reminder: example.com" || !strings.HasPrefix(notes[0].Text, "Renew the PositiveSSL subscription\n") {
		t.Errorf("notes %+v", notes)
	}
}

func TestNotifyTo(t *testing.T) {
	f := newFixture(t)
	a := f.notifier("team a", map[string]string{"locale": "en-US"})
	b := f.notifier("team b", map[string]string{"locale": "en-US"})
	f.svc.call = func(_ context.Context, via, method, path string, _, out any) error {
		if via != "notify" || method != http.MethodGet {
			t.Errorf("asked %s %s as %s", method, path, via)
		}
		switch path {
		case "/certs/certificates/3":
			return json.Unmarshal([]byte(`{"certificate":{"notifyTo":[`+strconv.FormatInt(b, 10)+`]}}`), out)
		case "/certs/certificates/4":
			return json.Unmarshal([]byte(`{"certificate":{"notifyTo":[]}}`), out)
		}
		return &module.CallError{Status: http.StatusNotFound}
	}
	for i, id := range []int64{3, 4, 5} {
		raw, _ := json.Marshal(contracts.CertificateEvent{CertificateID: id, Domains: []string{"example.com"}, Issuance: &contracts.Issuance{ID: 1}})
		ev := events.Event{ID: int64(i + 1), Type: contracts.CertificateIssueFailed, Source: "certs", Subject: strconv.FormatInt(id, 10), Data: raw, Time: start}
		if err := f.svc.handle(t.Context(), ev); err != nil {
			t.Fatal(err)
		}
	}
	// Certificate 3 tells team b only; 4 and 5, gone, everyone.
	if na, nb := len(f.notes(a)), len(f.notes(b)); na != 2 || nb != 3 {
		t.Errorf("team a %d notes, team b %d", na, nb)
	}
}

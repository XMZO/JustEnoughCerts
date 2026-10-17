package events

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fixture is a bus on a fresh database, with a fake clock and modules that
// can be switched off.
type fixture struct {
	t     *testing.T
	bus   *Bus
	db    *sql.DB
	clock *clock
	mu    sync.Mutex
	off   map[string]bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: st.DB(), clock: &clock{t: time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)}, off: map[string]bool{}}
	f.bus = New(st.DB(), slog.New(slog.DiscardHandler), func(m string) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return !f.off[m]
	})
	f.bus.now = f.clock.now
	return f
}

func (f *fixture) switchOff(module string, off bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.off[module] = off
}

func (f *fixture) subscribe(module string, sub Subscription) {
	f.t.Helper()
	if err := f.bus.Scope(module).Subscribe(sub); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) publish(module, typ, subject string, data any) {
	f.t.Helper()
	if err := f.bus.Scope(module).Publish(f.t.Context(), typ, subject, data); err != nil {
		f.t.Fatal(err)
	}
}

// flush delivers until nothing more is due now.
func (f *fixture) flush() {
	f.t.Helper()
	for range 100 {
		n, err := f.bus.dispatch(f.t.Context())
		if err != nil {
			f.t.Fatal(err)
		}
		f.bus.wg.Wait()
		if n == 0 {
			return
		}
	}
	f.t.Fatal("deliveries do not settle")
}

type deliveryRow struct {
	state     string
	attempts  int
	next      time.Time
	lastError string
}

func (f *fixture) delivery(subscriber string) deliveryRow {
	f.t.Helper()
	var r deliveryRow
	var next int64
	err := f.db.QueryRowContext(f.t.Context(),
		"SELECT state, attempts, next_at, last_error FROM kernel_deliveries WHERE subscriber = ? ORDER BY event_id DESC LIMIT 1",
		subscriber).Scan(&r.state, &r.attempts, &next, &r.lastError)
	if err != nil {
		f.t.Fatalf("delivery to %s: %v", subscriber, err)
	}
	r.next = time.UnixMilli(next).UTC()
	return r
}

func (f *fixture) count(table string) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRowContext(f.t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// recorder is a handler that keeps the events it gets and returns the
// errors it is given, one per call, then nil.
type recorder struct {
	mu     sync.Mutex
	events []Event
	errs   []error
}

func (r *recorder) handle(_ context.Context, ev Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	if len(r.errs) == 0 {
		return nil
	}
	err := r.errs[0]
	r.errs = r.errs[1:]
	return err
}

func (r *recorder) got() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

func TestPublishAndDeliver(t *testing.T) {
	f := newFixture(t)
	exact, prefix, all, other := &recorder{}, &recorder{}, &recorder{}, &recorder{}
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: exact.handle})
	f.subscribe("deploy", Subscription{Name: "certs", Types: []string{"certificate.*"}, Handler: prefix.handle})
	f.subscribe("audit", Subscription{Name: "everything", Types: []string{"*"}, Handler: all.handle})
	f.subscribe("other", Subscription{Name: "accounts", Types: []string{"account.created", "certificate.issued.late"}, Handler: other.handle})
	f.bus.Seal()

	f.publish("certs", "certificate.issued", "c1", map[string]string{"id": "c1"})
	f.flush()

	for name, r := range map[string]*recorder{"exact": exact, "prefix": prefix, "all": all} {
		got := r.got()
		if len(got) != 1 {
			t.Fatalf("%s handler got %d events", name, len(got))
		}
		ev := got[0]
		var data struct{ ID string }
		if err := ev.Decode(&data); err != nil {
			t.Fatal(err)
		}
		if ev.Type != "certificate.issued" || ev.Source != "certs" || ev.Subject != "c1" || data.ID != "c1" ||
			ev.Attempt != 1 || !ev.Time.Equal(f.clock.now()) || ev.ID == 0 {
			t.Errorf("%s handler got %+v", name, ev)
		}
	}
	if len(other.got()) != 0 {
		t.Error("a subscription to other types got the event")
	}
	for _, sub := range []string{"notify.issued", "deploy.certs", "audit.everything"} {
		if d := f.delivery(sub); d.state != "done" || d.attempts != 1 {
			t.Errorf("%s: %+v", sub, d)
		}
	}
	if n := f.count("kernel_deliveries"); n != 3 {
		t.Errorf("%d deliveries, want 3", n)
	}
}

func TestPublishTxRollback(t *testing.T) {
	f := newFixture(t)
	r := &recorder{}
	f.subscribe("notify", Subscription{Name: "all", Types: []string{"*"}, Handler: r.handle})
	f.bus.Seal()

	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.bus.Scope("certs").PublishTx(t.Context(), tx, "certificate.issued", "c1", nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	f.flush()
	if len(r.got()) != 0 || f.count("kernel_events") != 0 || f.count("kernel_deliveries") != 0 {
		t.Error("an event of a rolled back transaction was kept or delivered")
	}
}

func TestSeal(t *testing.T) {
	f := newFixture(t)
	r := &recorder{}
	if err := f.bus.Scope("certs").Publish(t.Context(), "certificate.issued", "", nil); err == nil {
		t.Error("published before the bus was sealed")
	}
	f.bus.Seal()
	err := f.bus.Scope("notify").Subscribe(Subscription{Name: "late", Types: []string{"*"}, Handler: r.handle})
	if err == nil {
		t.Error("subscribed after the bus was sealed")
	}
}

func TestSubscribeChecks(t *testing.T) {
	f := newFixture(t)
	h := (&recorder{}).handle
	f.subscribe("notify", Subscription{Name: "taken", Types: []string{"*"}, Handler: h})
	for name, sub := range map[string]Subscription{
		"duplicate":        {Name: "taken", Types: []string{"*"}, Handler: h},
		"bad name":         {Name: "Issued", Types: []string{"*"}, Handler: h},
		"no types":         {Name: "x", Handler: h},
		"no handler":       {Name: "x", Types: []string{"*"}},
		"negative":         {Name: "x", Types: []string{"*"}, Handler: h, MaxAge: -time.Second},
		"one word":         {Name: "x", Types: []string{"certificate"}, Handler: h},
		"uppercase":        {Name: "x", Types: []string{"Certificate.issued"}, Handler: h},
		"trailing dot":     {Name: "x", Types: []string{"certificate."}, Handler: h},
		"leading wildcard": {Name: "x", Types: []string{"*.issued"}, Handler: h},
		"double wildcard":  {Name: "x", Types: []string{"certificate.**"}, Handler: h},
	} {
		if err := f.bus.Scope("notify").Subscribe(sub); err == nil {
			t.Errorf("%s: subscribed", name)
		}
	}
	f.bus.Seal()
	for _, typ := range []string{"certificate", "Certificate.issued", "certificate.*", ""} {
		if err := f.bus.Scope("certs").Publish(t.Context(), typ, "", nil); err == nil {
			t.Errorf("published type %q", typ)
		}
	}
	if err := f.bus.Scope("certs").Publish(t.Context(), "certificate.issued", "", func() {}); err == nil {
		t.Error("published data that is not JSON")
	}
}

func TestRetryWithBackoff(t *testing.T) {
	f := newFixture(t)
	r := &recorder{errs: []error{errors.New("timeout"), errors.New("bad gateway")}}
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: r.handle})
	f.bus.Seal()
	f.publish("certs", "certificate.issued", "c1", nil)

	f.flush()
	d := f.delivery("notify.issued")
	if d.state != "pending" || d.attempts != 1 || d.lastError != "timeout" {
		t.Fatalf("after one failure: %+v", d)
	}
	if wait := d.next.Sub(f.clock.now()); wait < backoffBase/2 || wait > backoffBase {
		t.Errorf("first retry in %v", wait)
	}

	f.flush() // not due yet
	if n := len(r.got()); n != 1 {
		t.Fatalf("retried before the backoff ended: %d calls", n)
	}
	f.clock.add(backoffBase)
	f.flush()
	d = f.delivery("notify.issued")
	if wait := d.next.Sub(f.clock.now()); d.attempts != 2 || wait < backoffBase || wait > 2*backoffBase {
		t.Errorf("after two failures: %+v, retry in %v", d, wait)
	}
	f.clock.add(2 * backoffBase)
	f.flush()
	if d := f.delivery("notify.issued"); d.state != "done" || d.attempts != 3 {
		t.Errorf("after the third attempt: %+v", d)
	}
	var attempts []int
	for _, ev := range r.got() {
		attempts = append(attempts, ev.Attempt)
	}
	if len(attempts) != 3 || attempts[0] != 1 || attempts[2] != 3 {
		t.Errorf("attempts seen by the handler: %v", attempts)
	}
}

func TestRetryAfter(t *testing.T) {
	f := newFixture(t)
	r := &recorder{errs: []error{
		retry.After(errors.New("rate limited"), 2*time.Hour),
		retry.After(errors.New("rate limited"), 72*time.Hour),
	}}
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: r.handle})
	f.bus.Seal()
	f.publish("certs", "certificate.issued", "c1", nil)

	f.flush()
	if d := f.delivery("notify.issued"); d.next.Sub(f.clock.now()) != 2*time.Hour {
		t.Errorf("Retry-After of 2h: next attempt %v", d.next)
	}
	f.clock.add(2 * time.Hour)
	f.flush()
	if d := f.delivery("notify.issued"); d.next.Sub(f.clock.now()) != maxDelay {
		t.Errorf("Retry-After of 72h: next attempt %v, want the %v cap", d.next, maxDelay)
	}
}

func TestGiveUp(t *testing.T) {
	f := newFixture(t)
	always := &recorder{errs: []error{errors.New("down"), errors.New("still down"), errors.New("unreachable")}}
	permanent := &recorder{errs: []error{retry.Permanent(errors.New("payment required"))}}
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: always.handle, MaxAttempts: 2})
	f.subscribe("billing", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: permanent.handle})
	f.bus.Seal()
	f.publish("certs", "certificate.issued", "c1", nil)

	for range 3 {
		f.flush()
		f.clock.add(backoffMax)
	}
	if d := f.delivery("notify.issued"); d.state != "failed" || d.attempts != 2 || d.lastError != "still down" {
		t.Errorf("after MaxAttempts: %+v", d)
	}
	if n := len(always.got()); n != 2 {
		t.Errorf("%d attempts, want 2", n)
	}
	if d := f.delivery("billing.issued"); d.state != "failed" || d.attempts != 1 || len(permanent.got()) != 1 {
		t.Errorf("after a permanent error: %+v", d)
	}
}

func TestMaxAge(t *testing.T) {
	f := newFixture(t)
	r := &recorder{}
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: r.handle, MaxAge: time.Hour})
	f.bus.Seal()
	f.publish("certs", "certificate.issued", "c1", nil)
	f.clock.add(2 * time.Hour)
	f.flush()
	if d := f.delivery("notify.issued"); d.state != "expired" || d.attempts != 0 || len(r.got()) != 0 {
		t.Errorf("an event older than MaxAge: %+v, %d calls", d, len(r.got()))
	}
}

func TestModuleOff(t *testing.T) {
	f := newFixture(t)
	r := &recorder{}
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: r.handle})
	f.bus.Seal()
	f.switchOff("notify", true)
	f.publish("certs", "certificate.issued", "c1", nil)
	f.flush()
	if d := f.delivery("notify.issued"); d.state != "pending" || len(r.got()) != 0 {
		t.Fatalf("delivered to a module that is off: %+v", d)
	}
	f.switchOff("notify", false)
	f.flush()
	if d := f.delivery("notify.issued"); d.state != "done" || len(r.got()) != 1 {
		t.Errorf("not delivered once the module is back on: %+v", d)
	}
}

func TestPanic(t *testing.T) {
	f := newFixture(t)
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: func(context.Context, Event) error {
		panic("boom")
	}})
	f.bus.Seal()
	f.publish("certs", "certificate.issued", "c1", nil)
	f.flush()
	if d := f.delivery("notify.issued"); d.state != "pending" || d.attempts != 1 || d.lastError != "panic: boom" {
		t.Errorf("after a panic: %+v", d)
	}
}

func TestShutdown(t *testing.T) {
	f := newFixture(t)
	started := make(chan struct{})
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: func(ctx context.Context, _ Event) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}})
	f.bus.Seal()
	f.publish("certs", "certificate.issued", "c1", nil)

	ctx, cancel := context.WithCancel(t.Context())
	if n, err := f.bus.dispatch(ctx); n != 1 || err != nil {
		t.Fatalf("dispatch: %d, %v", n, err)
	}
	<-started
	cancel()
	f.bus.wg.Wait()
	if d := f.delivery("notify.issued"); d.state != "pending" || d.attempts != 0 || d.lastError != "" {
		t.Errorf("an attempt cut short by shutdown counted: %+v", d)
	}
}

func TestOneAtATime(t *testing.T) {
	f := newFixture(t)
	r := &recorder{}
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: r.handle})
	f.bus.Seal()
	f.publish("certs", "certificate.issued", "c1", nil)
	f.publish("certs", "certificate.issued", "c2", nil)

	if n, err := f.bus.dispatch(t.Context()); n != 1 || err != nil {
		t.Fatalf("dispatch started %d deliveries to one subscription, %v", n, err)
	}
	f.bus.wg.Wait()
	f.flush()
	got := r.got()
	if len(got) != 2 || got[0].Subject != "c1" || got[1].Subject != "c2" {
		t.Errorf("events out of order: %+v", got)
	}
}

func TestWorkers(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	for _, m := range []string{"a", "b", "c", "d", "e", "f"} {
		f.subscribe(m, Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: func(context.Context, Event) error {
			mu.Lock()
			calls++
			mu.Unlock()
			<-release
			return nil
		}})
	}
	f.bus.Seal()
	f.publish("certs", "certificate.issued", "c1", nil)
	if n, err := f.bus.dispatch(t.Context()); n != workers || err != nil {
		t.Errorf("dispatch started %d deliveries, want %d: %v", n, workers, err)
	}
	if n, _ := f.bus.dispatch(t.Context()); n != 0 {
		t.Errorf("dispatch started %d more while every worker was busy", n)
	}
	close(release)
	f.bus.wg.Wait()
	f.flush()
	if calls != 6 {
		t.Errorf("%d calls, want 6", calls)
	}
}

func TestPrune(t *testing.T) {
	f := newFixture(t)
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: (&recorder{}).handle})
	f.bus.Seal()
	f.publish("certs", "certificate.issued", "old", nil)
	f.clock.add(Retention)
	f.publish("certs", "certificate.issued", "new", nil)
	f.clock.add(time.Hour)
	if err := f.bus.Prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	var subject string
	if err := f.db.QueryRowContext(t.Context(), "SELECT subject FROM kernel_events").Scan(&subject); err != nil || subject != "new" {
		t.Errorf("after pruning: %q, %v", subject, err)
	}
	if n := f.count("kernel_deliveries"); n != 1 {
		t.Errorf("%d deliveries left, want 1", n)
	}
}

func TestRun(t *testing.T) {
	f := newFixture(t)
	f.bus.now = time.Now
	got := make(chan Event, 1)
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: func(_ context.Context, ev Event) error {
		got <- ev
		return nil
	}})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		f.bus.Run(ctx)
		close(done)
	}()
	// Run seals the bus; wait for that before publishing.
	for {
		f.bus.mu.Lock()
		sealed := f.bus.sealed
		f.bus.mu.Unlock()
		if sealed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	f.publish("certs", "certificate.issued", "c1", nil)
	select {
	case ev := <-got:
		if ev.Subject != "c1" {
			t.Errorf("got %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not deliver the event")
	}
	cancel()
	<-done
}

func TestErrorTruncated(t *testing.T) {
	f := newFixture(t)
	r := &recorder{errs: []error{errors.New(strings.Repeat("x", 3*maxErrorLen))}}
	f.subscribe("notify", Subscription{Name: "issued", Types: []string{"certificate.issued"}, Handler: r.handle})
	f.bus.Seal()
	f.publish("certs", "certificate.issued", "c1", nil)
	f.flush()
	if d := f.delivery("notify.issued"); len(d.lastError) != maxErrorLen {
		t.Errorf("stored %d bytes of the error", len(d.lastError))
	}
}

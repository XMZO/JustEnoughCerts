// Package events is the kernel's event bus (PLAN.md 4.5): modules publish
// what happened, and other modules react to it without importing each
// other.
//
// An event is stored in the transaction that makes the change it reports,
// together with one delivery for each subscription to its type, so an event
// is never lost and never sent for a change that was rolled back. Delivery
// is at least once and in no guaranteed order: a handler may see an event
// again after a crash or a failed attempt, and after a later event, so it
// must be idempotent and read the current state instead of trusting an
// event to be the latest. A failed delivery is tried again after a backoff;
// while a module is off, deliveries to it wait. Events are kept for
// Retention, delivered or not.
package events

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// Retention is how long events are kept, delivered or not.
const Retention = 30 * 24 * time.Hour

// Defaults of a Subscription.
const (
	DefaultMaxAttempts = 15
	DefaultTimeout     = time.Minute
)

const (
	workers     = 4                // deliveries in flight at most
	pollEvery   = time.Second      // how often due deliveries are looked for
	backoffBase = 10 * time.Second // the wait after a first failure
	backoffMax  = time.Hour        // the longest backoff
	maxDelay    = 24 * time.Hour   // the longest Retry-After honored
	maxErrorLen = 1000             // bytes of an error message stored
)

var (
	// Types are dotted lowercase words, as in certificate.issued.
	typePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	wordsPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)
	namePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// Event is something that happened, as a handler receives it.
type Event struct {
	ID int64
	// Type says what happened, as dotted lowercase words: the thing, then
	// what happened to it, as in certificate.issued.
	Type string
	// Source is the module that published the event, or "kernel".
	Source string
	// Subject is what the event is about, such as a certificate ID, or "".
	Subject string
	// Data is the payload, as JSON.
	Data json.RawMessage
	// Time is when the event was published.
	Time time.Time
	// Attempt counts the deliveries of this event to this handler, from 1.
	Attempt int
}

// Decode unmarshals the payload into v.
func (e Event) Decode(v any) error { return json.Unmarshal(e.Data, v) }

// Handler reacts to an event. When it returns an error, the bus tries again
// later: after a backoff, after the delay retry.After attached, or never
// for an error marked by retry.Permanent.
type Handler func(ctx context.Context, ev Event) error

// Subscription is a handler for some event types. It handles one event at a
// time, mostly in the order they were published.
type Subscription struct {
	// Name tells the module's subscriptions apart. Deliveries are stored
	// under it, so it must not change.
	Name string
	// Types are the event types to receive: a type, "certificate.*" for
	// every type below certificate, or "*" for all of them.
	Types []string
	// Handler is called for each event.
	Handler Handler
	// MaxAttempts bounds the attempts to deliver one event; after the last
	// one fails, the delivery is given up. Zero means DefaultMaxAttempts.
	MaxAttempts int
	// MaxAge drops events older than this instead of delivering them, such
	// as a notification that would come too late. Zero keeps trying for as
	// long as events are kept.
	MaxAge time.Duration
	// Timeout bounds one attempt. Zero means DefaultTimeout.
	Timeout time.Duration
}

// matches reports whether the subscription receives events of type typ.
func (s *Subscription) matches(typ string) bool {
	for _, p := range s.Types {
		base, prefix := strings.CutSuffix(p, ".*")
		switch {
		case p == "*", prefix && strings.HasPrefix(typ, base+"."), p == typ:
			return true
		}
	}
	return false
}

// validPattern reports whether p names event types for Subscription.Types.
func validPattern(p string) bool {
	if base, ok := strings.CutSuffix(p, ".*"); ok {
		return wordsPattern.MatchString(base)
	}
	return p == "*" || typePattern.MatchString(p)
}

// subscriber is a Subscription of a module.
type subscriber struct {
	Subscription
	id     string // <module>.<name>
	module string
}

// Bus stores events and delivers them to the subscriptions.
type Bus struct {
	db      *sql.DB
	log     *slog.Logger
	enabled func(module string) bool
	now     func() time.Time
	kick    chan struct{}

	mu     sync.Mutex
	sealed bool
	subs   map[string]*subscriber // by ID
	busy   map[string]bool        // subscribers with a delivery in flight
	wg     sync.WaitGroup
}

// New returns a bus on db, whose kernel migrations are applied. enabled
// reports whether a module is on; "kernel" counts as a module.
func New(db *sql.DB, log *slog.Logger, enabled func(module string) bool) *Bus {
	return &Bus{
		db: db, log: log, enabled: enabled, now: time.Now,
		kick: make(chan struct{}, 1),
		subs: map[string]*subscriber{}, busy: map[string]bool{},
	}
}

// Scope returns the handle through which module publishes and subscribes.
func (b *Bus) Scope(module string) *Scope { return &Scope{bus: b, module: module} }

// Seal ends the time for subscribing and starts the time for publishing,
// so that every event reaches every subscription. The kernel seals the bus
// once each module has registered.
func (b *Bus) Seal() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sealed = true
}

// Scope is a module's access to the bus.
type Scope struct {
	bus    *Bus
	module string
}

// Subscribe adds a subscription, before the bus is sealed.
func (s *Scope) Subscribe(sub Subscription) error {
	switch {
	case !namePattern.MatchString(sub.Name):
		return fmt.Errorf("subscription name %q: want lowercase letters, digits and underscores, starting with a letter", sub.Name)
	case len(sub.Types) == 0:
		return fmt.Errorf("subscription %s has no event types", sub.Name)
	case sub.Handler == nil:
		return fmt.Errorf("subscription %s has no handler", sub.Name)
	case sub.MaxAttempts < 0 || sub.MaxAge < 0 || sub.Timeout < 0:
		return fmt.Errorf("subscription %s: limits cannot be negative", sub.Name)
	}
	for _, p := range sub.Types {
		if !validPattern(p) {
			return fmt.Errorf("subscription %s: event type %q: want a type such as certificate.issued, a prefix such as certificate.*, or *", sub.Name, p)
		}
	}
	if sub.MaxAttempts == 0 {
		sub.MaxAttempts = DefaultMaxAttempts
	}
	if sub.Timeout == 0 {
		sub.Timeout = DefaultTimeout
	}
	sub.Types = slices.Clone(sub.Types)

	id := s.module + "." + sub.Name
	b := s.bus
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.sealed:
		return fmt.Errorf("subscription %s: subscribe while the module registers", id)
	case b.subs[id] != nil:
		return fmt.Errorf("subscription %s exists already", id)
	}
	b.subs[id] = &subscriber{Subscription: sub, id: id, module: s.module}
	return nil
}

// Publish stores an event in a transaction of its own; see PublishTx.
func (s *Scope) Publish(ctx context.Context, typ, subject string, data any) error {
	tx, err := s.bus.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("publish %s: %w", typ, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.PublishTx(ctx, tx, typ, subject, data); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("publish %s: %w", typ, err)
	}
	s.bus.wake()
	return nil
}

// PublishTx stores an event with its deliveries in tx, the transaction that
// makes the change the event reports, so that the event exists if and only
// if the change does. data is marshaled to JSON. The deliveries start once
// tx commits.
func (s *Scope) PublishTx(ctx context.Context, tx *sql.Tx, typ, subject string, data any) error {
	if !typePattern.MatchString(typ) {
		return fmt.Errorf("event type %q: want dotted lowercase words, as in certificate.issued", typ)
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("publish %s: %w", typ, err)
	}
	b := s.bus
	b.mu.Lock()
	sealed := b.sealed
	var subs []*subscriber
	for _, sub := range b.subs {
		if sub.matches(typ) {
			subs = append(subs, sub)
		}
	}
	b.mu.Unlock()
	if !sealed {
		return fmt.Errorf("publish %s: events can be published once every module has registered", typ)
	}
	slices.SortFunc(subs, func(a, b *subscriber) int { return strings.Compare(a.id, b.id) })

	now := b.now().UnixMilli()
	var id int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO kernel_events (type, source, subject, data, created_at) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		typ, s.module, subject, string(payload), now).Scan(&id)
	if err != nil {
		return fmt.Errorf("publish %s: %w", typ, err)
	}
	for _, sub := range subs {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kernel_deliveries (event_id, subscriber, module, state, attempts, next_at, last_error, updated_at)
			 VALUES (?, ?, ?, 'pending', 0, ?, '', ?)`,
			id, sub.id, sub.module, now, now)
		if err != nil {
			return fmt.Errorf("publish %s: %w", typ, err)
		}
	}
	b.wake()
	return nil
}

// wake makes Run look for due deliveries now.
func (b *Bus) wake() {
	select {
	case b.kick <- struct{}{}:
	default:
	}
}

// Run delivers events until ctx is done, then waits for the handlers it
// started, which see ctx done as well. It seals the bus.
func (b *Bus) Run(ctx context.Context) {
	b.Seal()
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		if _, err := b.dispatch(ctx); err != nil && ctx.Err() == nil {
			b.log.Warn("event dispatch failed", "err", err)
		}
		select {
		case <-ctx.Done():
			b.wg.Wait()
			return
		case <-t.C:
		case <-b.kick:
		}
	}
}

// due is the next delivery of a subscriber.
type due struct {
	sub    *subscriber
	ev     Event
	nextAt int64
}

// dispatch starts the earliest due delivery of each idle subscriber of a
// module that is on, as long as workers are free. It returns how many it
// started.
func (b *Bus) dispatch(ctx context.Context) (int, error) {
	if ctx.Err() != nil {
		return 0, nil
	}
	b.mu.Lock()
	free := workers - len(b.busy)
	var idle []*subscriber
	for id, sub := range b.subs {
		if !b.busy[id] && b.enabled(sub.module) {
			idle = append(idle, sub)
		}
	}
	b.mu.Unlock()
	if free <= 0 || len(idle) == 0 {
		return 0, nil
	}

	now := b.now().UnixMilli()
	var candidates []due
	for _, sub := range idle {
		d := due{sub: sub}
		var data string
		var created int64
		err := b.db.QueryRowContext(ctx,
			`SELECT d.attempts, d.next_at, e.id, e.type, e.source, e.subject, e.data, e.created_at
			 FROM kernel_deliveries d JOIN kernel_events e ON e.id = d.event_id
			 WHERE d.subscriber = ? AND d.state = 'pending' AND d.next_at <= ?
			 ORDER BY d.next_at, d.event_id LIMIT 1`,
			sub.id, now).Scan(&d.ev.Attempt, &d.nextAt, &d.ev.ID, &d.ev.Type, &d.ev.Source, &d.ev.Subject, &data, &created)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("due deliveries of %s: %w", sub.id, err)
		}
		d.ev.Attempt++
		d.ev.Data = json.RawMessage(data)
		d.ev.Time = time.UnixMilli(created)
		candidates = append(candidates, d)
	}
	slices.SortFunc(candidates, func(a, b due) int {
		return cmp.Or(cmp.Compare(a.nextAt, b.nextAt), cmp.Compare(a.ev.ID, b.ev.ID))
	})
	started := candidates[:min(free, len(candidates))]
	for _, d := range started {
		b.mu.Lock()
		b.busy[d.sub.id] = true
		b.wg.Add(1)
		b.mu.Unlock()
		go b.deliver(ctx, d.sub, d.ev)
	}
	return len(started), nil
}

// deliver hands ev to sub and records the outcome.
func (b *Bus) deliver(ctx context.Context, sub *subscriber, ev Event) {
	defer func() {
		b.mu.Lock()
		delete(b.busy, sub.id)
		b.mu.Unlock()
		b.wake()
		b.wg.Done()
	}()
	log := b.log.With("subscriber", sub.id, "event_id", ev.ID, "event", ev.Type, "attempt", ev.Attempt)
	if sub.MaxAge > 0 && b.now().Sub(ev.Time) > sub.MaxAge {
		log.Info("event too old to deliver, dropped", "age", b.now().Sub(ev.Time).Round(time.Second))
		b.record(ctx, sub, ev, "expired", ev.Attempt-1, 0, "")
		return
	}

	err := b.call(ctx, sub, ev)
	switch {
	case err == nil:
		b.record(ctx, sub, ev, "done", ev.Attempt, 0, "")
	case ctx.Err() != nil:
		// Shutting down: the attempt does not count, and the delivery stays due.
	case retry.IsPermanent(err) || ev.Attempt >= sub.MaxAttempts:
		log.Warn("event delivery failed, given up", "err", err)
		b.record(ctx, sub, ev, "failed", ev.Attempt, 0, err.Error())
	default:
		delay, ok := retry.Delay(err)
		if ok {
			delay = min(delay, maxDelay)
		} else {
			delay = retry.Backoff(ev.Attempt, backoffBase, backoffMax)
		}
		log.Info("event delivery failed, will retry", "err", err, "in", delay.Round(time.Second))
		b.record(ctx, sub, ev, "pending", ev.Attempt, delay, err.Error())
	}
}

// call runs the handler with the attempt's timeout, turning a panic into an
// error.
func (b *Bus) call(ctx context.Context, sub *subscriber, ev Event) (err error) {
	ctx, cancel := context.WithTimeout(ctx, sub.Timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("event handler panicked", "subscriber", sub.id, "event_id", ev.ID, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return sub.Handler(ctx, ev)
}

// record stores the outcome of an attempt. It runs even while shutting
// down, so that a handler that succeeded is not called again.
func (b *Bus) record(ctx context.Context, sub *subscriber, ev Event, state string, attempts int, delay time.Duration, msg string) {
	if len(msg) > maxErrorLen {
		msg = msg[:maxErrorLen]
	}
	now := b.now()
	_, err := b.db.ExecContext(context.WithoutCancel(ctx),
		`UPDATE kernel_deliveries SET state = ?, attempts = ?, next_at = ?, last_error = ?, updated_at = ?
		 WHERE event_id = ? AND subscriber = ?`,
		state, attempts, now.Add(delay).UnixMilli(), msg, now.UnixMilli(), ev.ID, sub.id)
	if err != nil {
		b.log.Error("record event delivery", "subscriber", sub.id, "event_id", ev.ID, "err", err)
	}
}

// Prune deletes the events older than Retention, with their deliveries.
func (b *Bus) Prune(ctx context.Context) error {
	res, err := b.db.ExecContext(ctx, "DELETE FROM kernel_events WHERE created_at < ?", b.now().Add(-Retention).UnixMilli())
	if err != nil {
		return fmt.Errorf("prune events: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		b.log.Info("pruned old events", "count", n)
	}
	return nil
}

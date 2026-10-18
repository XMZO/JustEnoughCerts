package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// How notes are sent.
const (
	sendTimeout = 30 * time.Second    // bounds sending one note
	keepSends   = 30 * 24 * time.Hour // what was sent is remembered as long as events are kept
)

// States of a send.
const (
	stateSent     = "sent"
	stateRetrying = "retrying" // tried again with the next delivery of the event
	stateFailed   = "failed"   // not tried again
)

type service struct {
	db   *sql.DB
	prov *providers.Scope
	log  *slog.Logger
	cat  *i18n.Catalog
	// base is the URL JEC is reached at, for links; "" when unknown.
	base string
	// zone is where the times in notes are told.
	zone *time.Location
	now  func() time.Time
}

// handle sends a note about ev through each notifier set to want it. A
// delivery of the event that comes again skips the notifiers that have it
// already. It fails, for the bus to try again later, while a notifier
// failed in a way that may pass.
func (s *service) handle(ctx context.Context, ev events.Event) error {
	var data contracts.CertificateEvent
	if err := ev.Decode(&data); err != nil {
		return retry.Permanent(fmt.Errorf("notify: event %d: %w", ev.ID, err))
	}
	list, err := s.prov.List(ctx, providers.Notifier)
	if err != nil {
		return err
	}
	var again []error
	for _, p := range list {
		if !wants(p.Config, ev.Type) {
			continue
		}
		done, err := s.done(ctx, ev.ID, p.ID)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		sendErr := s.send(ctx, p.ID, func(tag language.Tag) contracts.Note { return s.note(ctx, tag, ev, data) })
		if err := s.record(ctx, ev.ID, p.ID, ev.Type, sendErr); err != nil {
			return err
		}
		if sendErr != nil && !retry.IsPermanent(sendErr) {
			again = append(again, sendErr)
		}
	}
	return errors.Join(again...)
}

// wants reports whether a notifier set to config is told about events of
// type typ.
func wants(config map[string]string, typ string) bool {
	for _, e := range contracts.NotifyEvents {
		if e.Type != typ {
			continue
		}
		if v, ok := config[e.Key]; ok {
			return v == "true"
		}
		return e.Default
	}
	return false
}

// send writes a note in the language of notifier id and sends it there.
func (s *service) send(ctx context.Context, id int64, note func(language.Tag) contracts.Note) error {
	p, in, err := s.prov.Open(ctx, id)
	if err != nil {
		if errors.Is(err, providers.ErrNotFound) || errors.Is(err, providers.ErrUnavailable) {
			return retry.Permanent(err)
		}
		return err
	}
	n, ok := p.(contracts.Notifier)
	if !ok {
		if c, ok := p.(interface{ Close() error }); ok {
			_ = c.Close()
		}
		return retry.Permanent(fmt.Errorf("notify: provider %d is not a notifier", id))
	}
	defer func() { _ = n.Close() }()
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	return n.Notify(ctx, note(s.locale(in.Config[contracts.NoteLocale])))
}

// locale is the language a notifier is set to, or the closest JEC speaks.
func (s *service) locale(tag string) language.Tag {
	t, err := language.Parse(tag)
	if err != nil {
		return i18n.BaseLocale
	}
	return s.cat.Match(t)
}

// done reports whether notifier provider has the note about event eventID,
// or failed to get it for good.
func (s *service) done(ctx context.Context, eventID, provider int64) (bool, error) {
	var state string
	err := s.db.QueryRowContext(ctx, "SELECT state FROM notify_sends WHERE event_id = ? AND provider_id = ?", eventID, provider).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && state != stateRetrying, err
}

// record stores how sending the note about event eventID (0 for a test)
// through notifier provider went.
func (s *service) record(ctx context.Context, eventID, provider int64, event string, sendErr error) error {
	state, msg := stateSent, contracts.Message{}
	if sendErr != nil {
		state, msg = stateRetrying, problem(sendErr)
		if retry.IsPermanent(sendErr) {
			state = stateFailed
		}
		s.log.Warn("note not sent", "provider", provider, "event", event, "err", sendErr)
	}
	params := ""
	if len(msg.Params) > 0 {
		b, err := json.Marshal(msg.Params)
		if err != nil {
			return err
		}
		params = string(b)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notify_sends (event_id, provider_id, event, state, attempts, error_key, error_params, at)
		 VALUES (?, ?, ?, ?, 1, ?, ?, ?)
		 ON CONFLICT (event_id, provider_id) DO UPDATE SET event = excluded.event, state = excluded.state,
		 attempts = notify_sends.attempts + 1, error_key = excluded.error_key, error_params = excluded.error_params, at = excluded.at`,
		eventID, provider, event, state, string(msg.Key), params, s.now().UnixMilli())
	return err
}

// problem is what a failed send tells people: the notifier's own message,
// or a general one.
func problem(err error) contracts.Message {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return contracts.Message{Key: p.Key, Params: p.Params}
	}
	return contracts.Message{Key: i18n.NotifyErrorSend}
}

// prune forgets what was sent before the events it was about are gone.
func (s *service) prune(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM notify_sends WHERE at < ?", s.now().Add(-keepSends).UnixMilli())
	return err
}

// writer writes the lines of a note in one language.
type writer struct {
	s   *service
	tag language.Tag
}

func (w writer) text(key i18n.Key, args i18n.Args) string {
	text, err := w.s.cat.Format(w.tag, key, args)
	if err != nil {
		w.s.log.Warn("note text", "key", key, "err", err)
	}
	return text
}

// message is a message an event carries, such as why an issuance failed.
func (w writer) message(m contracts.Message) string {
	if m.Key == "" {
		return w.text(i18n.NotifyErrorUnknown, nil)
	}
	return w.text(m.Key, m.Params)
}

// retry says when the next attempt comes, or that none does.
func (w writer) retry(at time.Time) string {
	if at.IsZero() {
		return w.text(i18n.NotifyNoteGaveUp, nil)
	}
	return w.text(i18n.NotifyNoteRetry, i18n.Args{"time": at.In(w.s.zone).Format("2006-01-02 15:04 MST")})
}

// subject is title marked as JEC's.
func (w writer) subject(title string) string {
	return w.text(i18n.NotifyNoteSubject, i18n.Args{"title": title})
}

func (w writer) day(t time.Time) string { return t.In(w.s.zone).Format("2006-01-02") }

// note writes the note about ev in the language tag.
func (s *service) note(ctx context.Context, tag language.Tag, ev events.Event, d contracts.CertificateEvent) contracts.Note {
	w := writer{s: s, tag: tag}
	domain := ""
	if len(d.Domains) > 0 {
		domain = d.Domains[0]
	}
	n := contracts.Note{Event: ev.Type, Time: ev.Time, Data: ev.Data}
	cert := fmt.Sprintf("/certificates/%d", d.CertificateID)
	n.Link = s.link(cert)
	var lines []string
	switch ev.Type {
	case contracts.CertificateIssued:
		n.Title = w.text(i18n.NotifyNoteIssuedTitle, i18n.Args{"domain": domain})
		lines = append(lines, w.text(i18n.NotifyNoteValidUntil, i18n.Args{"date": w.day(d.NotAfter)}))
	case contracts.CertificateIssueFailed:
		n.Title = w.text(i18n.NotifyNoteIssueFailedTitle, i18n.Args{"domain": domain})
		if is := d.Issuance; is != nil {
			lines = append(lines, w.message(is.Error), w.retry(is.Retry))
			n.Link = s.link(fmt.Sprintf("%s/issuances/%d", cert, is.ID))
		}
	case contracts.CertificateDeployed, contracts.CertificateDeployFailed:
		target := w.text(i18n.NotifyNoteTargetGone, nil)
		if dep := d.Deployment; dep != nil {
			if p, err := s.prov.Get(ctx, dep.Target); err == nil {
				target = p.Name
			}
			if ev.Type == contracts.CertificateDeployFailed {
				lines = append(lines, w.message(dep.Error), w.retry(dep.Retry))
			}
			n.Link = s.link(fmt.Sprintf("%s/deployments/%d", cert, dep.ID))
		}
		title := i18n.NotifyNoteDeployedTitle
		if ev.Type == contracts.CertificateDeployFailed {
			title = i18n.NotifyNoteDeployFailedTitle
		}
		n.Title = w.text(title, i18n.Args{"domain": domain, "target": target})
	case contracts.CertificateServedOld:
		if c := d.Check; c != nil {
			n.Title = w.text(i18n.NotifyNoteServedOldTitle, i18n.Args{"domain": c.Domain})
			lines = append(lines, w.text(i18n.NotifyNoteServedOld, i18n.Args{"served": w.day(c.NotAfter), "current": w.day(d.NotAfter)}))
		}
	case contracts.CertificateExpiring:
		if d.Expiry == contracts.ExpiryExpired {
			n.Title = w.text(i18n.NotifyNoteExpiredTitle, i18n.Args{"domain": domain})
			lines = append(lines, w.text(i18n.NotifyNoteEnded, i18n.Args{"date": w.day(d.NotAfter)}))
		} else {
			days := max(int(d.NotAfter.Sub(s.now())/(24*time.Hour)), 0)
			n.Title = w.text(i18n.NotifyNoteExpiringTitle, i18n.Args{"domain": domain, "days": days})
			lines = append(lines, w.text(i18n.NotifyNoteEnds, i18n.Args{"date": w.day(d.NotAfter)}))
		}
	}
	if len(d.Domains) > 1 {
		lines = append(lines, w.text(i18n.NotifyNoteAlso, i18n.Args{"names": strings.Join(d.Domains[1:], ", ")}))
	}
	if n.Link != "" {
		lines = append(lines, w.text(i18n.NotifyNoteLink, i18n.Args{"url": n.Link}))
	}
	n.Text = strings.Join(lines, "\n")
	n.Subject = w.subject(n.Title)
	return n
}

// testNote is the note a test sends.
func (s *service) testNote(tag language.Tag) contracts.Note {
	w := writer{s: s, tag: tag}
	title := w.text(i18n.NotifyTestTitle, nil)
	return contracts.Note{
		Event: contracts.NoteTest, Time: s.now().UTC(),
		Title: title, Subject: w.subject(title), Text: w.text(i18n.NotifyTestText, nil),
	}
}

// link is the URL of a page of JEC, or "" when JEC does not know its own.
func (s *service) link(path string) string {
	if s.base == "" {
		return ""
	}
	return s.base + path
}

// Send is what was last sent through a notifier.
type Send struct {
	Provider int64
	Event    string
	State    string
	Attempts int
	Error    contracts.Message
	At       time.Time
}

// latest returns the last send through each notifier.
func (s *service) latest(ctx context.Context) ([]Send, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider_id, event, state, attempts, error_key, error_params, at FROM (
		SELECT *, row_number() OVER (PARTITION BY provider_id ORDER BY at DESC) AS n FROM notify_sends
	) WHERE n = 1 ORDER BY provider_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Send{}
	for rows.Next() {
		var x Send
		var params string
		var at int64
		// The key is scanned as it is: it was stored from a message.
		if err := rows.Scan(&x.Provider, &x.Event, &x.State, &x.Attempts, &x.Error.Key, &params, &at); err != nil {
			return nil, err
		}
		if params != "" {
			if err := json.Unmarshal([]byte(params), &x.Error.Params); err != nil {
				return nil, err
			}
		}
		x.At = time.UnixMilli(at).UTC()
		out = append(out, x)
	}
	return out, rows.Err()
}

package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
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
	// zone returns the time zone the times in notes are told in.
	zone func() *time.Location
	now  func() time.Time
	// call asks the API, for the notifiers a certificate's notes go to;
	// nil sends them to all.
	call func(ctx context.Context, via, method, path string, body, out any) error
}

// handle sends a note about ev through each notifier set to want it. A
// delivery of the event that comes again skips the notifiers that have it
// already. It fails, for the bus to try again later, while a notifier
// failed in a way that may pass.
func (s *service) handle(ctx context.Context, ev events.Event) error {
	var write func(language.Tag) contracts.Note
	var only []int64 // the notifiers the certificate's notes go to, if not all
	retried := false // a failed attempt between the first and the last
	switch ev.Type {
	case contracts.CertificatesDigest:
		var d contracts.Digest
		if err := ev.Decode(&d); err != nil {
			return retry.Permanent(fmt.Errorf("notify: event %d: %w", ev.ID, err))
		}
		extra := s.digestExtra(ctx)
		write = func(tag language.Tag) contracts.Note { return s.digestNote(tag, ev, d, extra) }
	case contracts.SiteExpiring:
		var d contracts.SiteEvent
		if err := ev.Decode(&d); err != nil {
			return retry.Permanent(fmt.Errorf("notify: event %d: %w", ev.ID, err))
		}
		write = func(tag language.Tag) contracts.Note { return s.siteNote(tag, ev, d) }
	case contracts.DomainExpiring:
		var d contracts.DomainEvent
		if err := ev.Decode(&d); err != nil {
			return retry.Permanent(fmt.Errorf("notify: event %d: %w", ev.ID, err))
		}
		write = func(tag language.Tag) contracts.Note { return s.domainNote(tag, ev, d) }
	case contracts.ReleaseAvailable:
		var d contracts.ReleaseEvent
		if err := ev.Decode(&d); err != nil {
			return retry.Permanent(fmt.Errorf("notify: event %d: %w", ev.ID, err))
		}
		write = func(tag language.Tag) contracts.Note { return s.releaseNote(tag, ev, d) }
	case contracts.ProviderFailing:
		var d contracts.ProviderEvent
		if err := ev.Decode(&d); err != nil {
			return retry.Permanent(fmt.Errorf("notify: event %d: %w", ev.ID, err))
		}
		write = func(tag language.Tag) contracts.Note { return s.providerNote(tag, ev, d) }
	case contracts.AccountSignedIn, contracts.AccountLocked:
		var d contracts.SignInEvent
		if err := ev.Decode(&d); err != nil {
			return retry.Permanent(fmt.Errorf("notify: event %d: %w", ev.ID, err))
		}
		write = func(tag language.Tag) contracts.Note { return s.accountNote(tag, ev, d) }
	case contracts.CertificateSpotted:
		var d contracts.SpottedEvent
		if err := ev.Decode(&d); err != nil {
			return retry.Permanent(fmt.Errorf("notify: event %d: %w", ev.ID, err))
		}
		write = func(tag language.Tag) contracts.Note { return s.spottedNote(tag, ev, d) }
	default:
		var data contracts.CertificateEvent
		if err := ev.Decode(&data); err != nil {
			return retry.Permanent(fmt.Errorf("notify: event %d: %w", ev.ID, err))
		}
		write = func(tag language.Tag) contracts.Note { return s.note(ctx, tag, ev, data) }
		var err error
		if only, err = s.notifyTo(ctx, data.CertificateID); err != nil {
			return err
		}
		failed := ev.Type == contracts.CertificateIssueFailed || ev.Type == contracts.CertificateDeployFailed
		retried = failed && data.Retried()
	}
	list, err := s.prov.List(ctx, providers.Notifier)
	if err != nil {
		return err
	}
	var again []error
	for _, p := range list {
		if !wants(p.Config, ev.Type) || len(only) > 0 && !slices.Contains(only, p.ID) || retried && p.Config[contracts.NoteRetries] != "true" {
			continue
		}
		done, err := s.done(ctx, ev.ID, p.ID)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		if until, quiet := quietUntil(p.Config, s.now(), s.zone()); quiet {
			if err := s.hold(ctx, ev.ID, p.ID, ev.Type, p.Config, write, until); err != nil {
				return err
			}
			continue
		}
		sendErr := s.send(ctx, p.ID, write)
		if err := s.record(ctx, ev.ID, p.ID, ev.Type, sendErr); err != nil {
			return err
		}
		if sendErr != nil && !retry.IsPermanent(sendErr) {
			again = append(again, sendErr)
		}
	}
	return errors.Join(again...)
}

// notifyTo returns the notifiers the notes of certificate id go to, nil for
// all, as the certs module tells; one it no longer knows goes to all.
func (s *service) notifyTo(ctx context.Context, id int64) ([]int64, error) {
	if s.call == nil || id == 0 {
		return nil, nil
	}
	var out struct {
		Certificate struct {
			NotifyTo []int64 `json:"notifyTo"`
		} `json:"certificate"`
	}
	err := s.call(ctx, "notify", http.MethodGet, "/certs/certificates/"+strconv.FormatInt(id, 10), nil, &out)
	var ce *module.CallError
	if errors.As(err, &ce) {
		return nil, nil
	}
	return out.Certificate.NotifyTo, err
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
	return w.text(i18n.NotifyNoteRetry, i18n.Args{"time": at.In(w.s.zone()).Format("2006-01-02 15:04 MST")})
}

// subject is title marked as JEC's.
func (w writer) subject(title string) string {
	return w.text(i18n.NotifyNoteSubject, i18n.Args{"title": title})
}

func (w writer) day(t time.Time) string { return t.In(w.s.zone()).Format("2006-01-02") }

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
	case contracts.CertificateChainIncomplete:
		if c := d.Check; c != nil {
			n.Title = w.text(i18n.NotifyNoteChainIncompleteTitle, i18n.Args{"domain": c.Domain})
			lines = append(lines, w.text(i18n.NotifyNoteChainIncomplete, nil))
		}
	case contracts.CertificateRevoked:
		n.Title = w.text(i18n.NotifyNoteRevokedTitle, i18n.Args{"domain": domain})
		if r := d.Revocation; r != nil {
			lines = append(lines, w.text(i18n.NotifyNoteRevokedWhen, i18n.Args{"date": w.day(r.At), "reason": w.text(contracts.RevocationReasonText(r.Reason), nil)}))
			if r.Renewing {
				lines = append(lines, w.text(i18n.NotifyNoteRevokedRenewing, nil))
			} else {
				lines = append(lines, w.text(i18n.NotifyNoteRevokedManual, nil))
			}
		}
	case contracts.CertificateReminder:
		n.Title = w.text(i18n.NotifyNoteReminderTitle, i18n.Args{"domain": domain})
		if d.Reminder != "" {
			lines = append(lines, d.Reminder)
		}
		if !d.NotAfter.IsZero() {
			lines = append(lines, w.text(i18n.NotifyNoteEnds, i18n.Args{"date": w.day(d.NotAfter)}))
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

// stateText names the states of a certificate, as the certs module tells
// them.
var stateText = map[string]i18n.Key{
	"ok": i18n.CertsStateOk, "expiring": i18n.CertsStateExpiring, "expired": i18n.CertsStateExpired,
	"issuing": i18n.CertsStateIssuing, "deploying": i18n.CertsStateDeploying,
	"failed": i18n.CertsStateFailed, "deploy_failed": i18n.CertsStateDeployFailed, "revoked": i18n.CertsStateRevoked,
	"awaiting": i18n.CertsStateAwaiting,
}

// digestNote writes the weekly digest in the language tag: what needs
// someone, and the renewals of the week to come.
func (s *service) digestNote(tag language.Tag, ev events.Event, d contracts.Digest, extra digestExtra) contracts.Note {
	w := writer{s: s, tag: tag}
	n := contracts.Note{Event: ev.Type, Time: ev.Time, Data: ev.Data, Link: s.link("/")}
	if len(d.Attention) == 0 {
		n.Title = w.text(i18n.NotifyNoteDigestTitleOk, i18n.Args{"total": d.Total})
	} else {
		n.Title = w.text(i18n.NotifyNoteDigestTitle, i18n.Args{"total": d.Total, "attention": len(d.Attention)})
	}
	var lines []string
	if len(d.Attention) > 0 {
		lines = append(lines, w.text(i18n.NotifyNoteDigestAttention, nil))
		for _, it := range d.Attention {
			state := it.State
			if key, ok := stateText[it.State]; ok {
				state = w.text(key, nil)
			}
			lines = append(lines, w.text(i18n.NotifyNoteDigestItem, i18n.Args{"domain": it.Domain, "state": state}))
		}
	}
	if len(d.Renewals) > 0 {
		lines = append(lines, w.text(i18n.NotifyNoteDigestRenewals, nil))
		for _, it := range d.Renewals {
			lines = append(lines, w.text(i18n.NotifyNoteDigestRenewal, i18n.Args{"domain": it.Domain, "time": it.RenewAt.In(s.zone()).Format("2006-01-02 15:04")}))
		}
	} else {
		lines = append(lines, w.text(i18n.NotifyNoteDigestNoRenewals, nil))
	}
	if len(d.Reminders) > 0 {
		lines = append(lines, w.text(i18n.NotifyNoteDigestReminders, nil))
		for _, it := range d.Reminders {
			lines = append(lines, w.text(i18n.NotifyNoteDigestReminder, i18n.Args{"domain": it.Domain, "date": it.RemindOn, "note": it.Reminder}))
		}
	}
	// What else needs someone, beyond the certificates.
	for _, it := range []struct {
		key   i18n.Key
		names []string
	}{
		{i18n.NotifyNoteDigestProviders, extra.providers},
		{i18n.NotifyNoteDigestSites, extra.sites},
		{i18n.NotifyNoteDigestDomains, extra.domains},
	} {
		if len(it.names) > 0 {
			lines = append(lines, w.text(it.key, i18n.Args{"names": strings.Join(it.names, ", ")}))
		}
	}
	if extra.spotted > 0 {
		lines = append(lines, w.text(i18n.NotifyNoteDigestSpotted, i18n.Args{"count": extra.spotted}))
	}
	if n.Link != "" {
		lines = append(lines, w.text(i18n.NotifyNoteLink, i18n.Args{"url": n.Link}))
	}
	n.Text = strings.Join(lines, "\n")
	n.Subject = w.subject(n.Title)
	return n
}

// siteNote writes the note about the certificate of a watched site.
func (s *service) siteNote(tag language.Tag, ev events.Event, d contracts.SiteEvent) contracts.Note {
	w := writer{s: s, tag: tag}
	site := d.Host
	if d.Port != 443 {
		site = net.JoinHostPort(d.Host, strconv.Itoa(d.Port))
	}
	if d.Name != "" {
		site = d.Name + " (" + site + ")"
	}
	n := contracts.Note{Event: ev.Type, Time: ev.Time, Data: ev.Data, Link: s.link("/sites")}
	if d.Expiry == contracts.ExpiryExpired {
		n.Title = w.text(i18n.NotifyNoteSiteExpiredTitle, i18n.Args{"site": site})
	} else {
		days := max(int(d.NotAfter.Sub(s.now())/(24*time.Hour)), 0)
		n.Title = w.text(i18n.NotifyNoteSiteExpiringTitle, i18n.Args{"site": site, "days": days})
	}
	lines := []string{w.text(i18n.NotifyNoteSiteEnds, i18n.Args{"date": w.day(d.NotAfter), "issuer": d.Issuer})}
	if n.Link != "" {
		lines = append(lines, w.text(i18n.NotifyNoteLink, i18n.Args{"url": n.Link}))
	}
	n.Text = strings.Join(lines, "\n")
	n.Subject = w.subject(n.Title)
	return n
}

// domainNote writes the note about the registration of a domain.
func (s *service) domainNote(tag language.Tag, ev events.Event, d contracts.DomainEvent) contracts.Note {
	w := writer{s: s, tag: tag}
	n := contracts.Note{Event: ev.Type, Time: ev.Time, Data: ev.Data, Link: s.link("/sites")}
	if d.Expiry == contracts.ExpiryExpired {
		n.Title = w.text(i18n.NotifyNoteDomainExpiredTitle, i18n.Args{"domain": d.Domain})
	} else {
		days := max(int(d.ExpiresAt.Sub(s.now())/(24*time.Hour)), 0)
		n.Title = w.text(i18n.NotifyNoteDomainExpiringTitle, i18n.Args{"domain": d.Domain, "days": days})
	}
	ends := w.text(i18n.NotifyNoteDomainEnds, i18n.Args{"date": w.day(d.ExpiresAt)})
	if d.Registrar != "" {
		ends = w.text(i18n.NotifyNoteDomainEndsAt, i18n.Args{"date": w.day(d.ExpiresAt), "registrar": d.Registrar})
	}
	lines := []string{ends}
	if len(d.Certificates) > 0 {
		lines = append(lines, w.text(i18n.NotifyNoteDomainCertificates, i18n.Args{"count": len(d.Certificates)}))
	}
	if n.Link != "" {
		lines = append(lines, w.text(i18n.NotifyNoteLink, i18n.Args{"url": n.Link}))
	}
	n.Text = strings.Join(lines, "\n")
	n.Subject = w.subject(n.Title)
	return n
}

// releaseNote writes the note about a new release of JEC.
func (s *service) releaseNote(tag language.Tag, ev events.Event, d contracts.ReleaseEvent) contracts.Note {
	w := writer{s: s, tag: tag}
	n := contracts.Note{Event: ev.Type, Time: ev.Time, Data: ev.Data, Link: d.URL}
	n.Title = w.text(i18n.NotifyNoteReleaseTitle, i18n.Args{"version": d.Version})
	lines := []string{w.text(i18n.NotifyNoteReleaseText, i18n.Args{"current": d.Current})}
	if d.URL != "" {
		lines = append(lines, w.text(i18n.NotifyNoteReleaseView, i18n.Args{"url": d.URL}))
	}
	n.Text = strings.Join(lines, "\n")
	n.Subject = w.subject(n.Title)
	return n
}

// providerNote writes the note about a provider that fails its daily test.
func (s *service) providerNote(tag language.Tag, ev events.Event, d contracts.ProviderEvent) contracts.Note {
	w := writer{s: s, tag: tag}
	pages := map[string]string{"source": "/sources", "solver": "/dns", "target": "/targets"}
	n := contracts.Note{Event: ev.Type, Time: ev.Time, Data: ev.Data, Link: s.link(pages[d.Kind])}
	n.Title = w.text(i18n.NotifyNoteProviderFailingTitle, i18n.Args{"name": d.Name})
	lines := []string{w.text(i18n.NotifyNoteProviderProblem, i18n.Args{"problem": w.message(d.Problem)}), w.text(i18n.NotifyNoteProviderHint, nil)}
	if n.Link != "" {
		lines = append(lines, w.text(i18n.NotifyNoteLink, i18n.Args{"url": n.Link}))
	}
	n.Text = strings.Join(lines, "\n")
	n.Subject = w.subject(n.Title)
	return n
}

// accountNote writes the note about a sign-in, or a client locked out.
func (s *service) accountNote(tag language.Tag, ev events.Event, d contracts.SignInEvent) contracts.Note {
	w := writer{s: s, tag: tag}
	n := contracts.Note{Event: ev.Type, Time: ev.Time, Data: ev.Data, Link: s.link("/settings")}
	var lines []string
	if ev.Type == contracts.AccountLocked {
		n.Title = w.text(i18n.NotifyNoteAccountLockedTitle, i18n.Args{"minutes": d.Minutes})
		if d.Username != "" {
			lines = append(lines, w.text(i18n.NotifyNoteAccountTried, i18n.Args{"username": d.Username}))
		}
	} else {
		n.Title = w.text(i18n.NotifyNoteAccountSignedInTitle, i18n.Args{"username": d.Username})
		if d.Via != "" {
			lines = append(lines, w.text(i18n.NotifyNoteAccountVia, i18n.Args{"via": d.Via}))
		}
	}
	lines = append(lines, w.text(i18n.NotifyNoteAccountFrom, i18n.Args{"client": d.Client}))
	if d.Device != "" {
		lines = append(lines, w.text(i18n.NotifyNoteAccountDevice, i18n.Args{"device": d.Device}))
	}
	lines = append(lines, w.text(i18n.NotifyNoteAccountHint, nil))
	if n.Link != "" {
		lines = append(lines, w.text(i18n.NotifyNoteLink, i18n.Args{"url": n.Link}))
	}
	n.Text = strings.Join(lines, "\n")
	n.Subject = w.subject(n.Title)
	return n
}

// spottedNote writes the note about a certificate found in certificate
// transparency logs that JEC does not hold.
func (s *service) spottedNote(tag language.Tag, ev events.Event, d contracts.SpottedEvent) contracts.Note {
	w := writer{s: s, tag: tag}
	n := contracts.Note{Event: ev.Type, Time: ev.Time, Data: ev.Data, Link: s.link("/sites")}
	n.Title = w.text(i18n.NotifyNoteSpottedTitle, i18n.Args{"domain": d.Domain})
	lines := []string{
		w.text(i18n.NotifyNoteSpottedNames, i18n.Args{"names": strings.Join(d.Names, ", ")}),
		w.text(i18n.NotifyNoteSpottedIssued, i18n.Args{"issuer": d.Issuer, "from": w.day(d.NotBefore), "to": w.day(d.NotAfter)}),
		w.text(i18n.NotifyNoteSpottedHint, nil),
	}
	if d.SHA256 != "" {
		lines = append(lines, w.text(i18n.NotifyNoteSpottedView, i18n.Args{"url": "https://crt.sh/?sha256=" + d.SHA256}))
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
	SendAt   time.Time // when a held note goes out
}

// latest returns the last send through each notifier.
func (s *service) latest(ctx context.Context) ([]Send, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider_id, event, state, attempts, error_key, error_params, at, send_at FROM (
		SELECT *, row_number() OVER (PARTITION BY provider_id ORDER BY at DESC) AS n FROM notify_sends
	) AS latest WHERE n = 1 ORDER BY provider_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Send{}
	for rows.Next() {
		var x Send
		var params string
		var at, sendAt int64
		// The key is scanned as it is: it was stored from a message.
		if err := rows.Scan(&x.Provider, &x.Event, &x.State, &x.Attempts, &x.Error.Key, &params, &at, &sendAt); err != nil {
			return nil, err
		}
		if x.State == stateHeld {
			x.SendAt = time.UnixMilli(sendAt).UTC()
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

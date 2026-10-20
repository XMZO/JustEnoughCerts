package certs

import (
	"context"
	"strconv"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

// expiryRank orders the expiry levels; a version is told about each level
// once, in this order, skipping the levels it passed while JEC was off.
var expiryRank = map[contracts.Expiry]int{"": 0, contracts.ExpiryWindow: 1, contracts.ExpiryUrgent: 2, contracts.ExpiryExpired: 3}

// expiry returns how close the end of a version valid from notBefore to
// notAfter is at now, for a renewal window of renewBeforeDays (limits).
func expiry(now, notBefore, notAfter time.Time, renewBeforeDays int) contracts.Expiry {
	window, urgent := limits(notBefore, notAfter, renewBeforeDays)
	switch left := notAfter.Sub(now); {
	case left <= 0:
		return contracts.ExpiryExpired
	case left <= urgent:
		return contracts.ExpiryUrgent
	case left <= window:
		return contracts.ExpiryWindow
	}
	return ""
}

// checkExpiry publishes certificate.expiring for each current version that
// reached a new expiry level since the last check.
func (s *service) checkExpiry(ctx context.Context) error {
	certs, err := s.list(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, c := range certs {
		if c.Current == nil {
			continue
		}
		level := expiry(now, c.Current.NotBefore, c.Current.NotAfter, c.RenewBeforeDays)
		if level == "" {
			continue
		}
		if err := s.notify(ctx, c, level); err != nil {
			return err
		}
	}
	return nil
}

// notify records level as told for the current version of c and publishes
// the event, unless that level or a later one was told already.
func (s *service) notify(ctx context.Context, c *Certificate, level contracts.Expiry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var told contracts.Expiry
	if err := tx.QueryRowContext(ctx, "SELECT notified FROM certs_versions WHERE id = ?", c.Current.ID).Scan(&told); err != nil {
		return err
	}
	if expiryRank[level] <= expiryRank[told] {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE certs_versions SET notified = ? WHERE id = ?", string(level), c.Current.ID); err != nil {
		return err
	}
	err = s.events.PublishTx(ctx, tx, contracts.CertificateExpiring, strconv.FormatInt(c.ID, 10), contracts.CertificateEvent{
		CertificateID: c.ID, Domains: c.Domains, VersionID: c.Current.ID, NotAfter: c.Current.NotAfter, Expiry: level,
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// remindAt is the hour of the day reminders go out, in the panel's time
// zone, as the weekly digest does.
const remindAt = 9

// checkReminders publishes certificate.reminder for each reminder whose
// day came, once.
func (s *service) checkReminders(ctx context.Context) error {
	certs, err := s.list(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, c := range certs {
		if c.ReminderOn == "" || c.ReminderSent {
			continue
		}
		day, err := time.ParseInLocation(time.DateOnly, c.ReminderOn, s.zone())
		if err != nil || now.Before(day.Add(remindAt*time.Hour)) {
			continue
		}
		if err := s.remind(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// remind marks the reminder of c sent and publishes it, unless it changed
// or went out meanwhile.
func (s *service) remind(ctx context.Context, c *Certificate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET reminder_sent = 1 WHERE id = ? AND reminder_on = ? AND reminder_sent = 0", c.ID, c.ReminderOn)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	d := contracts.CertificateEvent{CertificateID: c.ID, Domains: c.Domains, Reminder: c.ReminderNote, RemindOn: c.ReminderOn}
	if c.Current != nil {
		d.VersionID, d.NotAfter = c.Current.ID, c.Current.NotAfter
	}
	if err := s.events.PublishTx(ctx, tx, contracts.CertificateReminder, strconv.FormatInt(c.ID, 10), d); err != nil {
		return err
	}
	return tx.Commit()
}

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

// expiry returns how close notAfter is at now, for a renewal window of
// renewBeforeDays.
func expiry(now, notAfter time.Time, renewBeforeDays int) contracts.Expiry {
	switch left := notAfter.Sub(now); {
	case left <= 0:
		return contracts.ExpiryExpired
	case left <= urgentWithin:
		return contracts.ExpiryUrgent
	case left <= time.Duration(renewBeforeDays)*day:
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
		level := expiry(now, c.Current.NotAfter, c.RenewBeforeDays)
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

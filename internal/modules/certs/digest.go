package certs

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

// The weekly digest: on Monday from nine, in the panel's time zone, how the
// certificates stand, once a week (contracts.CertificatesDigest). The week
// told last is a setting, so that a restart tells it no second time.
const (
	digestDay         = time.Monday
	digestHour        = 9
	digestSoon        = 7 * day
	settingDigestWeek = "digest_week"
)

// digest publishes the digest of this week, when it is due and not told.
func (s *service) digest(ctx context.Context) error {
	now := s.now().In(s.zone())
	if now.Weekday() != digestDay || now.Hour() < digestHour {
		return nil
	}
	year, w := now.ISOWeek()
	week := fmt.Sprintf("%d-W%02d", year, w)
	var told string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM certs_settings WHERE key = ?", settingDigestWeek).Scan(&told)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if told == week {
		return nil
	}
	certs, err := s.list(ctx)
	if err != nil {
		return err
	}
	sch, err := s.schedule(ctx)
	if err != nil {
		return err
	}
	d := digestOf(certs, now, sch)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO certs_settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		settingDigestWeek, week); err != nil {
		return err
	}
	if err := s.events.PublishTx(ctx, tx, contracts.CertificatesDigest, week, d); err != nil {
		return err
	}
	return tx.Commit()
}

// digestOf sums certs up at now.
func digestOf(certs []*Certificate, now time.Time, sch schedule) contracts.Digest {
	d := contracts.Digest{Total: len(certs), Attention: []contracts.DigestItem{}, Renewals: []contracts.DigestItem{}}
	for _, c := range certs {
		state := c.state(now)
		item := contracts.DigestItem{CertificateID: c.ID, State: state}
		if len(c.Domains) > 0 {
			item.Domain = c.Domains[0]
		}
		if c.Current != nil {
			item.NotAfter = c.Current.NotAfter
		}
		switch {
		case state == stateOK:
			d.OK++
		case state == stateFailed, state == stateDeployFailed, state == stateExpired,
			state == stateExpiring && c.Source != sourceProvider:
			d.Attention = append(d.Attention, item)
		}
		if c.Current != nil && c.Source == sourceProvider {
			if at := sch.due(c); at.Before(now.Add(digestSoon)) && state != stateFailed {
				item.RenewAt = at
				d.Renewals = append(d.Renewals, item)
			}
		}
	}
	slices.SortFunc(d.Renewals, func(a, b contracts.DigestItem) int { return a.RenewAt.Compare(b.RenewAt) })
	slices.SortFunc(d.Attention, func(a, b contracts.DigestItem) int { return cmp.Compare(a.Domain, b.Domain) })
	return d
}

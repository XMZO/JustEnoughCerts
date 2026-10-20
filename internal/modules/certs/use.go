package certs

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

// Going back to an earlier version (PLAN.md 4.6): when a new version
// causes trouble, as a chain some clients do not take, an earlier one that
// has not expired or been revoked, for the same names, becomes current
// again and is deployed to every target. The renewal goes by it from then
// on, so it may come soon.

var (
	errUseCurrent = errors.New("certs: the version is in use already")
	errUseEnded   = errors.New("certs: the version has expired or been revoked")
	errUseNames   = errors.New("certs: the version is for other names than the certificate")
)

// usable tells why version v of c cannot become current again, or nil
// when it can.
func usable(c *Certificate, v *Version, now time.Time) error {
	switch {
	case c.Current != nil && c.Current.ID == v.ID:
		return errUseCurrent
	case !now.Before(v.NotAfter) || !v.RevokedAt.IsZero() || !v.CARevokedAt.IsZero():
		return errUseEnded
	}
	names, want := slices.Clone(v.Names), slices.Clone(c.Domains)
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		return errUseNames
	}
	return nil
}

// useVersion makes version of certificate id current again, deploys it
// to every target and tells the other modules (CertificateCurrent).
func (s *service) useVersion(ctx context.Context, id, version int64) error {
	versions, err := s.versions(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.load(ctx, tx, id)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(versions, func(v *Version) bool { return v.ID == version })
	if i < 0 {
		return errNotFound
	}
	v := versions[i]
	if err := usable(c, v, s.now()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET current_version_id = ?, updated_at = ? WHERE id = ?",
		v.ID, s.now().UnixMilli(), id); err != nil {
		return err
	}
	targets, err := s.targetIDs(ctx, tx, id)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := s.queueDeployment(ctx, tx, id, v.ID, target, deployManual); err != nil {
			return err
		}
	}
	if err := s.events.PublishTx(ctx, tx, contracts.CertificateCurrent, strconv.FormatInt(id, 10), contracts.CertificateEvent{
		CertificateID: id, Domains: c.Domains, VersionID: v.ID, Fingerprint: v.Fingerprint, NotAfter: v.NotAfter.UTC(),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.log.Info("went back to an earlier version", "certificate", id, "version", v.ID, "serial", v.Serial)
	s.deploying.kick()
	return nil
}

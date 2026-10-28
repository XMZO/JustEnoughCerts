package certs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// Revoking (PLAN.md 4.6): a version that a source which revokes
// (contracts.Revoker, as an ACME account) issued can be revoked at its CA,
// once the password is confirmed. Not the version in use: a new one
// replaces it first, so that no site is left serving a revoked
// certificate. Nor a version that has expired, which CAs do not revoke.

var (
	errRevokeCurrent = errors.New("certs: the version is in use")
	errRevokeSource  = errors.New("certs: no source that revokes issued the version")
	errRevokeExpired = errors.New("certs: the version has expired")
	errRevoked       = errors.New("certs: the version is revoked already")
)

// revocationReasons are the reasons a version can be revoked for, by the
// names of the API.
var revocationReasons = func() map[string]contracts.RevocationReason {
	m := map[string]contracts.RevocationReason{}
	for _, r := range []contracts.RevocationReason{contracts.ReasonUnspecified, contracts.ReasonKeyCompromise,
		contracts.ReasonSuperseded, contracts.ReasonCessationOfOperation} {
		m[r.String()] = r
	}
	return m
}()

// revocable tells why version v of c cannot be revoked now, or nil when
// it can. typeOf tells the types of sources.
func revocable(c *Certificate, v *Version, now time.Time, typeOf func(id int64) (providers.Type, error)) error {
	switch {
	case !v.RevokedAt.IsZero():
		return errRevoked
	case c.Current != nil && c.Current.ID == v.ID:
		return errRevokeCurrent
	case !now.Before(v.NotAfter):
		return errRevokeExpired
	case v.Origin != originIssued || v.SourceProvider == 0:
		return errRevokeSource
	}
	t, err := typeOf(v.SourceProvider)
	if err != nil {
		return err
	}
	if !t.Revocable {
		return errRevokeSource
	}
	return nil
}

// typeCache returns the types of sources, asking the registry once for
// each.
func (s *service) typeCache(ctx context.Context) func(id int64) (providers.Type, error) {
	types := map[int64]providers.Type{}
	return func(id int64) (providers.Type, error) {
		if t, ok := types[id]; ok {
			return t, nil
		}
		t, err := s.providers.Type(ctx, id)
		if err != nil {
			return providers.Type{}, err
		}
		types[id] = t
		return t, nil
	}
}

// revoke has version of certificate id revoked at its CA, for reason.
func (s *service) revoke(ctx context.Context, id, version int64, reason string) error {
	code, ok := revocationReasons[reason]
	if !ok {
		return fmt.Errorf("certs: no revocation reason %q", reason)
	}
	c, err := s.load(ctx, s.db, id)
	if err != nil {
		return err
	}
	versions, err := s.versions(ctx, id)
	if err != nil {
		return err
	}
	var v *Version
	for _, each := range versions {
		if each.ID == version {
			v = each
		}
	}
	if v == nil {
		return errNotFound
	}
	if err := revocable(c, v, s.now(), s.typeCache(ctx)); err != nil {
		return err
	}
	return s.revokeVersion(ctx, id, v, code)
}

// revokeVersion has version v of certificate id revoked at the CA of the
// source that issued it, for code, and records it.
func (s *service) revokeVersion(ctx context.Context, id int64, v *Version, code contracts.RevocationReason) error {
	chain, err := cryptox.ParseCertificates([]byte(v.Chain))
	if err != nil {
		return err
	}
	revoker, err := openProvider[contracts.Revoker](ctx, s.providers, v.SourceProvider)
	if err != nil {
		return err
	}
	if p, ok := revoker.(io.Closer); ok {
		defer closeProvider(p, s)
	}
	if err := revoker.Revoke(ctx, chain[0], code); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE certs_versions SET revoked_at = ?, revoke_reason = ? WHERE id = ?",
		s.now().UnixMilli(), code.String(), v.ID); err != nil {
		return err
	}
	s.log.Info("revoked a version", "certificate", id, "version", v.ID, "serial", v.Serial, "reason", code.String())
	return nil
}

// stillRevocable tells whether JEC can still have version v revoked at
// its CA: one a source that revokes issued, not revoked yet, not expired.
// typeOf tells the types of sources.
func stillRevocable(v *Version, now time.Time, typeOf func(id int64) (providers.Type, error)) bool {
	if !v.RevokedAt.IsZero() || !v.CARevokedAt.IsZero() || !now.Before(v.NotAfter) || v.Origin != originIssued || v.SourceProvider == 0 {
		return false
	}
	t, err := typeOf(v.SourceProvider)
	return err == nil && t.Revocable
}

// revokeAnyReason has version v of certificate id revoked for code, or,
// should its CA not take that reason, for none given: when nobody is
// there to choose another.
func (s *service) revokeAnyReason(ctx context.Context, id int64, v *Version, code contracts.RevocationReason) error {
	err := s.revokeVersion(ctx, id, v, code)
	if errors.Is(err, contracts.ErrReasonRefused) && code != contracts.ReasonUnspecified {
		s.log.Info("the CA does not take the reason: revoking for none", "certificate", id, "version", v.ID, "reason", code.String())
		err = s.revokeVersion(ctx, id, v, contracts.ReasonUnspecified)
	}
	return err
}

// retire has every version of certificate id that its CA would still
// revoke revoked, for reason, the one in use included, then deletes the
// certificate: for a site that is no more. It returns how many it had
// revoked. A CA that does not take the reason revokes for none given
// instead, since nobody is there to choose another; a version the CA
// does not revoke at all stops it, before anything is deleted and before
// the one in use is revoked; one whose source is gone, or does not
// revoke, is left as it is.
func (s *service) retire(ctx context.Context, id int64, reason string) (int, error) {
	code, ok := revocationReasons[reason]
	if !ok {
		return 0, fmt.Errorf("certs: no revocation reason %q", reason)
	}
	versions, err := s.versions(ctx, id)
	if err != nil {
		return 0, err
	}
	typeOf := s.typeCache(ctx)
	now, revoked := s.now(), 0
	// The oldest first, so that a refusal leaves the one in use valid.
	slices.Reverse(versions)
	for _, v := range versions {
		if !stillRevocable(v, now, typeOf) {
			continue
		}
		if err := s.revokeAnyReason(ctx, id, v, code); err != nil {
			return revoked, err
		}
		revoked++
	}
	return revoked, s.remove(ctx, id)
}

// What revokeAll does with the certificate afterwards.
const (
	thenPause = "pause" // pauses its renewal, as for a site that is no more but whose history is kept
	thenRenew = "renew" // has it issued again at once, as for a key that may have leaked
)

// errNothingRevocable is a certificate with no version JEC can still have
// revoked.
var errNothingRevocable = errors.New("certs: no version can be revoked")

// revokeAll has every version of certificate id that its CA would still
// revoke revoked, for reason, the one in use last, and keeps the
// certificate, doing then with it. It returns how many it had revoked. A
// CA that refuses a version stops it, leaving the one in use valid if it
// had not come to it; to pause, the renewal is paused before the one in
// use goes, so that it does not start for it, and not if that fails.
func (s *service) revokeAll(ctx context.Context, id int64, reason, then string) (int, error) {
	code, ok := revocationReasons[reason]
	if !ok {
		return 0, fmt.Errorf("certs: no revocation reason %q", reason)
	}
	c, err := s.load(ctx, s.db, id)
	if err != nil {
		return 0, err
	}
	if then == thenRenew && c.Source != sourceProvider {
		return 0, errNoSource
	}
	versions, err := s.versions(ctx, id)
	if err != nil {
		return 0, err
	}
	typeOf := s.typeCache(ctx)
	now := s.now()
	slices.Reverse(versions)
	versions = slices.DeleteFunc(versions, func(v *Version) bool { return !stillRevocable(v, now, typeOf) })
	if len(versions) == 0 {
		return 0, errNothingRevocable
	}
	pause := then == thenPause && !c.Paused
	setPaused := func(on bool) error {
		paused := 0
		if on {
			paused = 1
		}
		_, err := s.db.ExecContext(ctx, "UPDATE certs_certificates SET paused = ?, updated_at = ? WHERE id = ?", paused, s.now().UnixMilli(), id)
		return err
	}
	revoked := 0
	for _, v := range versions {
		current := c.Current != nil && v.ID == c.Current.ID
		if current && pause {
			if err := setPaused(true); err != nil {
				return revoked, err
			}
		}
		if err := s.revokeVersion(ctx, id, v, code); err != nil {
			if current && pause {
				err = errors.Join(err, setPaused(false))
			}
			return revoked, err
		}
		if current {
			pause = false
		}
		revoked++
	}
	if pause {
		// The one in use is not one JEC can have revoked.
		if err := setPaused(true); err != nil {
			return revoked, err
		}
	}
	if then == thenRenew {
		if _, err := s.issue(ctx, id); err != nil {
			return revoked, err
		}
	}
	return revoked, nil
}

// revokeOldWait is how long after it came the version in use of a
// certificate without targets is taken to have reached the servers that
// pull it or read it from a directory.
const revokeOldWait = 24 * time.Hour

// revokeOldRetry is how long after a failure JEC asks a CA again to revoke
// an earlier version.
const revokeOldRetry = 24 * time.Hour

// deployedEverywhere tells whether the version in use of c has reached its
// servers: deployed to every target, or, without targets, revokeOldWait
// after it came.
func deployedEverywhere(c *Certificate, now time.Time) bool {
	if len(c.Targets) == 0 {
		return !now.Before(c.Current.CreatedAt.Add(revokeOldWait))
	}
	for _, target := range c.Targets {
		i := slices.IndexFunc(c.Deployments, func(d *Deployment) bool { return d.Target == target })
		if i < 0 || c.Deployments[i].VersionID != c.Current.ID || c.Deployments[i].State != issuanceDone {
			return false
		}
	}
	return true
}

// checkRevokeOld is the job certs.revoke_old. For each certificate set to
// (RevokeOld), once its version in use has reached its servers, it has the
// versions before that one which JEC can still have revoked revoked as
// superseded, or for no reason should the CA not take that one. A version
// the CA does not revoke is asked about again revokeOldRetry later.
func (s *service) checkRevokeOld(ctx context.Context) error {
	certs, err := s.list(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	typeOf := s.typeCache(ctx)
	for _, c := range certs {
		cur := c.Current
		if !c.RevokeOld || cur == nil || !cur.RevokedAt.IsZero() || !cur.CARevokedAt.IsZero() || !deployedEverywhere(c, now) {
			continue
		}
		versions, err := s.versions(ctx, c.ID)
		if err != nil {
			return err
		}
		for _, v := range versions {
			if v.ID == cur.ID || !v.CreatedAt.Before(cur.CreatedAt) || now.Sub(v.RevokeTried) < revokeOldRetry || !stillRevocable(v, now, typeOf) {
				continue
			}
			err := s.revokeAnyReason(ctx, c.ID, v, contracts.ReasonSuperseded)
			switch {
			case err == nil:
			case ctx.Err() != nil:
				return ctx.Err()
			default:
				s.log.Warn("revoke an earlier version", "certificate", c.ID, "version", v.ID, "err", err)
				if _, err := s.db.ExecContext(ctx, "UPDATE certs_versions SET revoke_tried = ? WHERE id = ?", now.UnixMilli(), v.ID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

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

// retire has every version of certificate id that its CA would still
// revoke revoked, for reason, the one in use included, then deletes the
// certificate: for a site that is no more. It returns how many it had
// revoked. A version the CA does not revoke stops it, before anything is
// deleted and before the one in use is revoked; one whose source is gone,
// or does not revoke, is left as it is.
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
		if !v.RevokedAt.IsZero() || !v.CARevokedAt.IsZero() || !now.Before(v.NotAfter) || v.Origin != originIssued || v.SourceProvider == 0 {
			continue
		}
		if t, err := typeOf(v.SourceProvider); err != nil || !t.Revocable {
			continue
		}
		if err := s.revokeVersion(ctx, id, v, code); err != nil {
			return revoked, err
		}
		revoked++
	}
	return revoked, s.remove(ctx, id)
}

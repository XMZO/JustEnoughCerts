package certs

import (
	"context"
	"errors"
	"fmt"
	"io"
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
		s.now().UnixMilli(), reason, v.ID); err != nil {
		return err
	}
	s.log.Info("revoked a version", "certificate", id, "version", v.ID, "serial", v.Serial, "reason", reason)
	return nil
}

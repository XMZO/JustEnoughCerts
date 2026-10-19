package certs

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"math/rand/v2"
	"net/url"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// How long until JEC asks a CA again about renewing a version: as the CA
// asks, but between an hour and a day, and six hours when it does not
// say; a week when it makes no suggestions; an hour after a failure.
const (
	renewalInfoDefault = 6 * time.Hour
	renewalInfoMin     = time.Hour
	renewalInfoMax     = 24 * time.Hour
	renewalInfoNone    = 7 * 24 * time.Hour
	renewalInfoRetry   = time.Hour
)

// replaced is the leaf of the current version of c when the source of c
// issued it: the certificate its renewal replaces, as an ACME order says
// (RFC 9773), and the one to ask that source about. Nil otherwise.
func replaced(c *Certificate) *x509.Certificate {
	v := c.Current
	if v == nil || v.SourceProvider == 0 || v.SourceProvider != c.SourceProvider {
		return nil
	}
	chain, err := cryptox.ParseCertificates([]byte(v.Chain))
	if err != nil || len(chain) == 0 {
		return nil
	}
	return chain[0]
}

// due is when the renewal of the current version of c starts: by the
// schedule, or earlier when its CA suggests so, as before it revokes the
// version, and at once once it did. A later suggestion does not hold the
// renewal back.
func (sch schedule) due(c *Certificate) time.Time {
	if !c.Current.CARevokedAt.IsZero() {
		return c.Current.CARevokedAt
	}
	at := sch.renewAt(c.Current.NotBefore, c.Current.NotAfter, c.RenewBeforeDays)
	if r := c.Current.Renewal; r != nil && r.At.Before(at) {
		return r.At
	}
	return at
}

// checkRenewalInfo is the job certs.renewal_info. For each certificate
// whose current version its source issued, it asks the source, when due,
// what the CA suggests about renewing it (ACME Renewal Information), and
// keeps the window with a time picked at random in it, as RFC 9773 has
// clients do. The renew job starts the renewal at that time, if it comes
// before the schedule.
func (s *service) checkRenewalInfo(ctx context.Context) error {
	certs, err := s.list(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, c := range certs {
		leaf := replaced(c)
		if c.Source != sourceProvider || leaf == nil || now.Before(c.Current.RenewalCheck) || !now.Before(c.Current.NotAfter) {
			continue
		}
		if err := s.askRenewalInfo(ctx, c, leaf, now); err != nil {
			return err
		}
	}
	return nil
}

// askRenewalInfo asks the source of c about renewing leaf, its current
// version, and stores the answer with the time to ask again.
func (s *service) askRenewalInfo(ctx context.Context, c *Certificate, leaf *x509.Certificate, now time.Time) error {
	v := c.Current
	provider, err := openProvider[io.Closer](ctx, s.providers, c.SourceProvider)
	if err != nil {
		// A source turned off, say: ask later.
		return s.renewalCheck(ctx, v.ID, now.Add(renewalInfoDefault))
	}
	defer closeProvider(provider, s)
	advisor, ok := provider.(contracts.RenewalAdvisor)
	if !ok {
		return s.renewalCheck(ctx, v.ID, now.Add(renewalInfoNone))
	}
	info, err := advisor.RenewalInfo(ctx, leaf)
	switch {
	case errors.Is(err, contracts.ErrNoRenewalInfo):
		return s.renewalCheck(ctx, v.ID, now.Add(renewalInfoNone))
	case err != nil && ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		s.log.Warn("ask about renewing", "certificate", c.ID, "err", err)
		return s.renewalCheck(ctx, v.ID, now.Add(renewalInfoRetry))
	case !info.End.After(info.Start):
		s.log.Warn("the CA suggests no window to renew in", "certificate", c.ID, "start", info.Start, "end", info.End)
		return s.renewalCheck(ctx, v.ID, now.Add(renewalInfoDefault))
	}
	at := info.Start.Add(rand.N(info.End.Sub(info.Start))) //nolint:gosec // G404: spreading renewals out needs no secret randomness
	if r := v.Renewal; r != nil && r.Start.Equal(info.Start) && r.End.Equal(info.End) {
		at = r.At // the same window: the same time in it
	} else {
		s.log.Info("the CA suggests when to renew", "certificate", c.ID, "start", info.Start, "end", info.End, "explanation", info.ExplanationURL)
	}
	wait := info.RetryAfter
	if wait == 0 {
		wait = renewalInfoDefault
	}
	wait = min(max(wait, renewalInfoMin), renewalInfoMax)
	// Only a web page goes to the UI, which links to it.
	explanation := ""
	if u, err := url.Parse(info.ExplanationURL); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		explanation = u.String()
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE certs_versions SET renewal_start = ?, renewal_end = ?, renewal_at = ?, renewal_explanation = ?, renewal_check = ?
		 WHERE id = ?`, info.Start.UnixMilli(), info.End.UnixMilli(), at.UnixMilli(), explanation, now.Add(wait).UnixMilli(), v.ID)
	return err
}

// renewalCheck sets when to ask about renewing version id again.
func (s *service) renewalCheck(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, "UPDATE certs_versions SET renewal_check = ? WHERE id = ?", at.UnixMilli(), id)
	return err
}

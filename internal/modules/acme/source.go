package acme

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	legoacme "github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/registration"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// source is an ACME account at work.
type source struct {
	m  *Module
	in providers.Instance
}

var _ contracts.Source = (*source)(nil)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	return &source{m: m, in: in}, nil
}

// Close has nothing to release.
func (*source) Close() error { return nil }

// Issue registers the account if it is new, then orders the certificate
// for req.Key, with req.Solver answering the DNS-01 challenges. Its errors
// are Problems where the cause is known, marked for retrying or not.
func (s *source) Issue(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if req.Solver == nil {
		return nil, retry.Permanent(fmt.Errorf("ACME needs a solver for %s", strings.Join(req.Domains, ", ")))
	}
	directory, err := directoryOf(s.in)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	key, location, err := s.m.account(ctx, s.in.ID, directory)
	if err != nil {
		return nil, err
	}
	u := &user{email: s.in.Config["email"], key: key}
	if location != "" {
		u.reg = &legoacme.ExtendedAccount{Location: location}
	}
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AcmeLogConnect, Params: map[string]any{"directory": directory}})
	cfg := lego.NewConfig(u)
	cfg.CADirURL = directory
	cfg.UserAgent = "JustEnoughCerts/" + buildinfo.Get().Version
	cfg.HTTPClient = s.m.client()
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, &contracts.Problem{Key: i18n.AcmeErrorDirectory, Params: map[string]any{"directory": directory}, Err: err}
	}

	if u.reg == nil {
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AcmeLogRegister, Params: map[string]any{"directory": directory}})
		reg, err := s.register(ctx, client)
		if err != nil {
			return nil, err
		}
		if err := s.m.registered(ctx, s.in.ID, reg.Location); err != nil {
			return nil, err
		}
		u.reg = reg
	}

	p := &dnsProvider{solver: req.Solver, log: log, skipCheck: s.m.skipPropagation, missed: map[string]bool{}}
	if err := client.Challenge.SetDNS01Provider(p, dns01.WrapPreCheck(p.precheck)); err != nil {
		return nil, err
	}
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AcmeLogOrder, Params: map[string]any{"domains": strings.Join(req.Domains, ", ")}})
	res, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{Domains: req.Domains, PrivateKey: req.Key, Bundle: true})
	if err != nil {
		if isAccountGone(err) {
			// Register again next time: the CA forgot the account.
			if err := s.m.registered(context.WithoutCancel(ctx), s.in.ID, ""); err != nil {
				s.m.log.Warn("forget the ACME account", "provider", s.in.ID, "err", err)
			}
		}
		return nil, explain(err, p.unseen())
	}
	chain, err := cryptox.ParseCertificates(res.Certificate)
	if err != nil {
		return nil, err
	}
	log(contracts.LogLine{Level: contracts.OK, Step: contracts.StepDownload, Key: i18n.AcmeLogIssued})
	return chain, nil
}

// register makes the account known to the CA, with External Account
// Binding when the account has it.
func (s *source) register(ctx context.Context, client *lego.Client) (*legoacme.ExtendedAccount, error) {
	kid, hmac := s.in.Config["eab_kid"], s.in.Secrets["eab_hmac"]
	var reg *legoacme.ExtendedAccount
	var err error
	switch {
	case kid != "" && hmac != "":
		reg, err = client.Registration.RegisterWithExternalAccountBinding(ctx, registration.RegisterEABOptions{
			TermsOfServiceAgreed: true, Kid: kid, HmacEncoded: eabHMAC(hmac),
		})
	case client.GetServerMetadata().ExternalAccountRequired:
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.AcmeErrorEabRequired})
	default:
		reg, err = client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
	}
	if err != nil {
		problem := &contracts.Problem{Key: i18n.AcmeErrorRegister, Err: err}
		var pd *legoacme.ProblemDetails
		if errors.As(err, &pd) && pd.Type != legoacme.ServerInternalErrorType && pd.Type != legoacme.RateLimitedErrorType {
			return nil, retry.Permanent(problem) // the CA refused: fix the account first
		}
		return nil, problem
	}
	return reg, nil
}

func isAccountGone(err error) bool {
	var pd *legoacme.ProblemDetails
	return errors.As(err, &pd) && pd.Type == legoacme.AccountDoesNotExistErrorType
}

// explain turns an error of lego's Obtain into a Problem where the cause is
// known, marked for retrying, retrying after the CA's delay, or not.
// unseen lists the domains whose validation records never became visible.
func explain(err error, unseen []string) error {
	var problem *contracts.Problem
	if errors.As(err, &problem) {
		return err // from the solver
	}
	var limited *legoacme.RateLimitedError
	if errors.As(err, &limited) {
		return retry.After(&contracts.Problem{Key: i18n.AcmeErrorRateLimited, Err: err}, limited.RetryAfter)
	}
	var pd *legoacme.ProblemDetails
	if errors.As(err, &pd) {
		typ, domain := pd.Type, ""
		if typ == legoacme.CompoundErrorType && len(pd.SubProblems) > 0 {
			typ, domain = pd.SubProblems[0].Type, pd.SubProblems[0].Identifier.Value
		}
		switch typ {
		case legoacme.ExternalAccountRequiredErrorType:
			return retry.Permanent(&contracts.Problem{Key: i18n.AcmeErrorEabRequired, Err: err})
		case legoacme.RejectedIdentifierErrorType, legoacme.UnsupportedIdentifierErrorType:
			return retry.Permanent(&contracts.Problem{Key: i18n.AcmeErrorRejected, Err: err})
		case legoacme.UserActionRequiredErrorType:
			return retry.Permanent(&contracts.Problem{Key: i18n.AcmeErrorUserAction, Params: map[string]any{"url": pd.Instance}, Err: err})
		case legoacme.CaaErrorType:
			return &contracts.Problem{Key: i18n.AcmeErrorCaa, Params: map[string]any{"domain": domain}, Err: err}
		case legoacme.UnauthorizedErrorType, legoacme.IncorrectResponseErrorType, legoacme.DNSErrorType,
			legoacme.ConnectionErrorType, legoacme.TLSErrorType:
			return &contracts.Problem{Key: i18n.AcmeErrorValidation, Err: err}
		}
		return &contracts.Problem{Key: i18n.AcmeErrorCa, Err: err}
	}
	// lego's wait for the records gives up with this text only.
	if strings.Contains(err.Error(), "time limit exceeded") && len(unseen) > 0 {
		return &contracts.Problem{Key: i18n.AcmeErrorPropagation, Params: map[string]any{"domains": strings.Join(unseen, ", ")}, Err: err}
	}
	return err
}

// dnsProvider answers lego's DNS-01 challenges through a contracts.Solver,
// and checks that the answers show before lego tells the CA.
type dnsProvider struct {
	solver    contracts.Solver
	log       func(contracts.LogLine)
	skipCheck bool // trust the records to show, as for tests

	mu      sync.Mutex
	waiting bool            // the wait was logged
	missed  map[string]bool // domains not seen yet, each logged once; false once seen
}

func (p *dnsProvider) Present(ctx context.Context, domain, token, keyAuth string) error {
	p.log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepChallenge, Key: i18n.AcmeLogPresent, Params: map[string]any{"domain": domain}})
	err := p.solver.Present(ctx, contracts.Challenge{Domain: domain, Token: token, KeyAuth: keyAuth})
	var problem *contracts.Problem
	if err != nil && !errors.As(err, &problem) {
		err = &contracts.Problem{Key: i18n.AcmeErrorPresent, Params: map[string]any{"domain": domain}, Err: err}
	}
	return err
}

// CleanUp removes an answer. lego only logs a failure, so it shows in the
// issuance's log here.
func (p *dnsProvider) CleanUp(ctx context.Context, domain, token, keyAuth string) error {
	p.log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepCleanup, Key: i18n.AcmeLogCleanup, Params: map[string]any{"domain": domain}})
	err := p.solver.CleanUp(ctx, contracts.Challenge{Domain: domain, Token: token, KeyAuth: keyAuth})
	if err != nil {
		p.log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepCleanup, Key: i18n.AcmeLogCleanupFailed, Params: map[string]any{"domain": domain}, Detail: err.Error()})
	}
	return err
}

// Timeout is the solver's own timing, which lego waits by. lego asks for
// it as it starts waiting for the answers to show.
func (p *dnsProvider) Timeout() (time.Duration, time.Duration) {
	timeout, interval := p.solver.Wait()
	p.mu.Lock()
	first := !p.waiting
	p.waiting = true
	p.mu.Unlock()
	if first {
		p.log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepValidate, Key: i18n.AcmeLogWait, Params: map[string]any{"seconds": int(timeout / time.Second)}})
	}
	return timeout, interval
}

// precheck wraps lego's check that an answer shows in DNS: it stops the
// wait once ctx is done, which lego's own wait does not notice, and logs
// the first miss of each domain.
func (p *dnsProvider) precheck(ctx context.Context, domain, fqdn, value string, check dns01.PreCheckFunc) (bool, error) {
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if p.skipCheck {
		return true, nil
	}
	found, err := check(ctx, fqdn, value)
	p.mu.Lock()
	_, missedBefore := p.missed[domain]
	p.missed[domain] = !found
	p.mu.Unlock()
	if !found && !missedBefore {
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		p.log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepValidate, Key: i18n.AcmeLogNotVisible, Params: map[string]any{"domain": domain}, Detail: detail})
	}
	return found, err
}

// unseen returns the domains whose answers never showed, in order.
func (p *dnsProvider) unseen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for d, missing := range p.missed {
		if missing {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out
}

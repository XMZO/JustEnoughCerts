package acme

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	legoacme "github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/acme/api"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/go-acme/lego/v5/challenge/http01"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/registration"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// source is an ACME account at work.
type source struct {
	m  *Module
	in providers.Instance
}

var (
	_ contracts.Source         = (*source)(nil)
	_ contracts.Tester         = (*source)(nil)
	_ contracts.RenewalAdvisor = (*source)(nil)
)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	return &source{m: m, in: in}, nil
}

// Close has nothing to release.
func (*source) Close() error { return nil }

// Issue registers the account if it is new, then orders the certificate
// for req.Key, with req.Solver answering DNS-01 challenges or req.Files
// HTTP-01 ones. Its errors are Problems where the cause is known, marked
// for retrying or not.
func (s *source) Issue(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if req.Solver == nil && req.Files == nil {
		return nil, retry.Permanent(fmt.Errorf("ACME needs a solver for %s", strings.Join(req.Domains, ", ")))
	}
	if dir, err := directoryOf(s.in); err == nil {
		if err := checkCAA(ctx, s.m.caa, dir, req.Domains, log); err != nil {
			return nil, err
		}
	}
	client, _, err := s.connect(ctx, log)
	if err != nil {
		return nil, err
	}

	p := &dnsProvider{solver: req.Solver, log: log, skipCheck: s.m.skipPropagation, missed: map[string]bool{}}
	if req.Files != nil {
		if err := client.Challenge.SetHTTP01Provider(&fileProvider{files: req.Files, log: log}); err != nil {
			return nil, err
		}
	} else if err := client.Challenge.SetDNS01Provider(p, dns01.WrapPreCheck(p.precheck)); err != nil {
		return nil, err
	}
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AcmeLogOrder, Params: map[string]any{"domains": strings.Join(req.Domains, ", ")}})
	order := certificate.ObtainRequest{Domains: req.Domains, PrivateKey: req.Key, Bundle: true, Profile: strings.TrimSpace(s.in.Config["profile"])}
	if req.Replaces != nil {
		// The order renews the current version (RFC 9773), which lego says
		// to a CA with ARI only.
		order.ReplacesCertID, _ = api.MakeARICertID(req.Replaces)
	}
	res, err := client.Certificate.Obtain(ctx, order)
	if err != nil && order.ReplacesCertID != "" && refusedReplaces(err) {
		// The CA does not take the order as the renewal: the version came
		// from another account, say. Nothing was validated yet.
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AcmeLogReplacesRefused, Detail: err.Error()})
		order.ReplacesCertID = ""
		res, err = client.Certificate.Obtain(ctx, order)
	}
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

// refusedReplaces tells whether err is the CA refusing a new order for its
// replaces field, as Let's Encrypt does for a certificate of another
// account or of another CA, rather than a failure of the validation.
// lego itself orders again when the certificate was replaced already.
func refusedReplaces(err error) bool {
	var pd *legoacme.ProblemDetails
	if !errors.As(err, &pd) || pd.Method != http.MethodPost || pd.Type == legoacme.RateLimitedErrorType || pd.Type == legoacme.ServerInternalErrorType {
		return false
	}
	// As Boulder words it: "request included `replaces` field, but no
	// current certificate with serial ... exists", "while parsing ARI
	// CertID an error occurred".
	detail := strings.ToLower(pd.Detail)
	return strings.Contains(detail, "replace") || strings.Contains(detail, "certid")
}

// RenewalInfo asks the CA what it suggests about renewing leaf, which
// this account issued (RFC 9773). A CA without ARI, or one that does not
// know leaf, makes no suggestions.
func (s *source) RenewalInfo(ctx context.Context, leaf *x509.Certificate) (contracts.RenewalInfo, error) {
	client, _, err := s.connect(ctx, func(contracts.LogLine) {})
	if err != nil {
		return contracts.RenewalInfo{}, err
	}
	info, err := client.Certificate.GetRenewalInfo(ctx, leaf)
	var pd *legoacme.ProblemDetails
	switch {
	case errors.Is(err, api.ErrNoARI), errors.As(err, &pd) && pd.HTTPStatus == http.StatusNotFound:
		return contracts.RenewalInfo{}, contracts.ErrNoRenewalInfo
	case err != nil:
		return contracts.RenewalInfo{}, err
	}
	return contracts.RenewalInfo{Start: info.SuggestedWindow.Start, End: info.SuggestedWindow.End,
		ExplanationURL: info.ExplanationURL, RetryAfter: info.RetryAfter}, nil
}

// Revoke has the CA revoke leaf, which this account issued (RFC 8555 7.6),
// signing the request with the account's key. One the CA has revoked
// already is no error.
func (s *source) Revoke(ctx context.Context, leaf *x509.Certificate, reason contracts.RevocationReason) error {
	client, _, err := s.connect(ctx, func(contracts.LogLine) {})
	if err != nil {
		return err
	}
	code := uint(reason)
	err = client.Certificate.RevokeWithReason(ctx, cryptox.EncodeCertificates(leaf), &code)
	var pd *legoacme.ProblemDetails
	switch {
	case err == nil:
		return nil
	case errors.As(err, &pd) && pd.Type == legoacme.AlreadyRevokedErrorType:
		return nil
	case errors.As(err, &pd) && pd.Type == legoacme.BadRevocationReasonErrorType:
		return &contracts.Problem{Key: i18n.AcmeErrorRevokeReason, Err: err}
	case errors.As(err, &pd):
		return &contracts.Problem{Key: i18n.AcmeErrorRevoke, Params: map[string]any{"detail": pd.Detail}, Err: err}
	}
	return err
}

// Test reaches the CA and registers the account if it is new, as the
// first issuance would.
func (s *source) Test(ctx context.Context) (contracts.Message, error) {
	client, directory, err := s.connect(ctx, func(contracts.LogLine) {})
	if err != nil {
		return contracts.Message{}, err
	}
	// The profiles the CA offers, for the field that picks one, which has
	// to be among them.
	offered := slices.Sorted(maps.Keys(client.GetServerMetadata().Profiles))
	if p := strings.TrimSpace(s.in.Config["profile"]); p != "" && !slices.Contains(offered, p) {
		return contracts.Message{}, &contracts.Problem{Key: i18n.AcmeErrorProfile, Params: map[string]any{"profile": p, "profiles": strings.Join(offered, ", ")}}
	}
	if len(offered) > 0 {
		return contracts.Message{Key: i18n.AcmeTestProfiles, Params: map[string]any{"directory": directory, "profiles": strings.Join(offered, ", ")}}, nil
	}
	return contracts.Message{Key: i18n.AcmeTestOk, Params: map[string]any{"directory": directory}}, nil
}

// connect makes the client of the account at its CA, registering the
// account first if the CA does not know it yet, and returns the client
// with the CA's directory URL.
func (s *source) connect(ctx context.Context, log func(contracts.LogLine)) (*lego.Client, string, error) {
	directory, err := directoryOf(s.in)
	if err != nil {
		return nil, "", retry.Permanent(err)
	}
	key, location, err := s.m.account(ctx, s.in.ID, directory)
	if err != nil {
		return nil, "", err
	}
	u := &user{email: s.in.Config["email"], key: key}
	if location != "" {
		u.reg = &legoacme.ExtendedAccount{Location: location}
	}
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AcmeLogConnect, Params: map[string]any{"directory": directory}})
	cfg := lego.NewConfig(u)
	cfg.CADirURL = directory
	cfg.UserAgent = "JustEnoughCerts/" + buildinfo.Get().Version
	if cfg.HTTPClient, err = s.m.client(s.in.Config[httpx.ProxyKey]); err != nil {
		return nil, "", err
	}
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, "", &contracts.Problem{Key: i18n.AcmeErrorDirectory, Params: map[string]any{"directory": directory}, Err: err}
	}
	if u.reg == nil {
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AcmeLogRegister, Params: map[string]any{"directory": directory}})
		reg, err := s.register(ctx, client)
		if err != nil {
			return nil, "", err
		}
		if err := s.m.registered(ctx, s.in.ID, reg.Location); err != nil {
			return nil, "", err
		}
		u.reg = reg
	}
	return client, directory, nil
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
	// The record itself, for people to check, or to add by hand (RFC 8555
	// section 8.4).
	sum := sha256.Sum256([]byte(keyAuth))
	p.log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepChallenge, Key: i18n.AcmeLogRecord, Params: map[string]any{
		"name": "_acme-challenge." + domain, "value": base64.RawURLEncoding.EncodeToString(sum[:]),
	}})
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

// fileProvider answers lego's HTTP-01 challenges through a
// contracts.FileSolver: the CA fetches /.well-known/acme-challenge/<token>
// from the domain and gets the key authorization.
type fileProvider struct {
	files contracts.FileSolver
	log   func(contracts.LogLine)
}

func challengeFile(domain, token, keyAuth string) contracts.File {
	return contracts.File{Domain: domain, Path: http01.ChallengePath(token), Content: []byte(keyAuth)}
}

func (p *fileProvider) Present(ctx context.Context, domain, token, keyAuth string) error {
	p.log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepChallenge, Key: i18n.AcmeLogPutFile, Params: map[string]any{"domain": domain}})
	err := p.files.PutFile(ctx, challengeFile(domain, token, keyAuth))
	var problem *contracts.Problem
	if err != nil && !errors.As(err, &problem) {
		err = &contracts.Problem{Key: i18n.AcmeErrorPutFile, Params: map[string]any{"domain": domain}, Err: err}
	}
	return err
}

// CleanUp removes a file. lego only logs a failure, so it shows in the
// issuance's log here.
func (p *fileProvider) CleanUp(ctx context.Context, domain, token, keyAuth string) error {
	p.log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepCleanup, Key: i18n.AcmeLogRemoveFile, Params: map[string]any{"domain": domain}})
	err := p.files.RemoveFile(ctx, challengeFile(domain, token, keyAuth))
	if err != nil {
		p.log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepCleanup, Key: i18n.AcmeLogRemoveFileFailed, Params: map[string]any{"domain": domain}, Detail: err.Error()})
	}
	return err
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

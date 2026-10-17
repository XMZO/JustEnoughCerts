package acme

import (
	"context"
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	legoacme "github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/registration"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
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
// for req.Key, with req.Solver answering the DNS-01 challenges.
func (s *source) Issue(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	directory, err := directoryOf(s.in)
	if err != nil {
		return nil, err
	}
	key, location, err := s.m.account(ctx, s.in.ID, directory)
	if err != nil {
		return nil, err
	}
	u := &user{email: s.in.Config["email"], key: key}
	if location != "" {
		u.reg = &legoacme.ExtendedAccount{Location: location}
	}
	cfg := lego.NewConfig(u)
	cfg.CADirURL = directory
	cfg.UserAgent = "JustEnoughCerts/" + buildinfo.Get().Version
	cfg.HTTPClient = s.m.client()
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("ACME directory %s: %w", directory, err)
	}

	if u.reg == nil {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AcmeLogRegister, Params: map[string]any{"directory": directory}})
		reg, err := s.register(ctx, client)
		if err != nil {
			return nil, err
		}
		if err := s.m.registered(ctx, s.in.ID, reg.Location); err != nil {
			return nil, err
		}
		u.reg = reg
	}

	if req.Solver == nil {
		return nil, fmt.Errorf("ACME needs a solver for %s", strings.Join(req.Domains, ", "))
	}
	if err := client.Challenge.SetDNS01Provider(&dnsProvider{solver: req.Solver, log: log}, s.m.dnsOptions...); err != nil {
		return nil, err
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.AcmeLogOrder, Params: map[string]any{"domains": strings.Join(req.Domains, ", ")}})
	res, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{Domains: req.Domains, PrivateKey: req.Key, Bundle: true})
	if err != nil {
		return nil, err
	}
	chain, err := cryptox.ParseCertificates(res.Certificate)
	if err != nil {
		return nil, err
	}
	log(contracts.LogLine{Level: contracts.OK, Key: i18n.AcmeLogIssued})
	return chain, nil
}

// register makes the account known to the CA, with External Account
// Binding when the account has it.
func (s *source) register(ctx context.Context, client *lego.Client) (*legoacme.ExtendedAccount, error) {
	kid, hmac := s.in.Config["eab_kid"], s.in.Secrets["eab_hmac"]
	if kid != "" && hmac != "" {
		return client.Registration.RegisterWithExternalAccountBinding(ctx, registration.RegisterEABOptions{
			TermsOfServiceAgreed: true, Kid: kid, HmacEncoded: eabHMAC(hmac),
		})
	}
	if client.GetServerMetadata().ExternalAccountRequired {
		return nil, &contracts.Problem{Key: i18n.AcmeErrorEabRequired}
	}
	return client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
}

// dnsProvider answers lego's DNS-01 challenges through a contracts.Solver.
type dnsProvider struct {
	solver contracts.Solver
	log    func(contracts.LogLine)
}

func (p *dnsProvider) Present(ctx context.Context, domain, token, keyAuth string) error {
	p.log(contracts.LogLine{Level: contracts.Info, Key: i18n.AcmeLogPresent, Params: map[string]any{"domain": domain}})
	return p.solver.Present(ctx, contracts.Challenge{Domain: domain, Token: token, KeyAuth: keyAuth})
}

func (p *dnsProvider) CleanUp(ctx context.Context, domain, token, keyAuth string) error {
	p.log(contracts.LogLine{Level: contracts.Info, Key: i18n.AcmeLogCleanup, Params: map[string]any{"domain": domain}})
	return p.solver.CleanUp(ctx, contracts.Challenge{Domain: domain, Token: token, KeyAuth: keyAuth})
}

// Timeout is the solver's own timing, which lego waits by.
func (p *dnsProvider) Timeout() (time.Duration, time.Duration) { return p.solver.Wait() }

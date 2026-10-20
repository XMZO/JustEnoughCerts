package providersapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/net/idna"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

// A trial of a solver of DNS does what an issuance would, without a CA:
// it adds the TXT record of an ACME challenge for a domain, waits for the
// domain's authoritative servers to answer it, and removes it. So the
// settings of any of the DNS providers can be tried before a certificate
// depends on them. Each step is bounded, for the request to end within
// what reverse proxies wait.
const (
	tryPresent = 40 * time.Second
	tryVisible = 40 * time.Second
	tryLook    = 4 * time.Second
	tryCleanUp = 30 * time.Second
)

// visible tells whether the authoritative servers of a name answer a
// record; delegated where _acme-challenge under a domain points. Tests
// replace them.
var (
	visible   = dnscheck.Visible
	delegated = dnscheck.Delegated
)

type tryInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Domain string `json:"domain" minLength:"1" maxLength:"253" doc:"A domain whose DNS the provider manages, as example.com; the record goes to _acme-challenge under it"`
	}
}

// TryBody is how a trial of a solver of DNS went.
type TryBody struct {
	Record  string `json:"record" doc:"The record's name, as _acme-challenge.example.com"`
	Via     string `json:"via,omitempty" doc:"Where the record went, when _acme-challenge under the domain is a CNAME to that name"`
	Value   string `json:"value" doc:"The TXT value added"`
	Visible bool   `json:"visible" doc:"The domain's authoritative servers answered it within the time a trial waits"`
	Seconds int    `json:"seconds" doc:"How long it took to be added and become visible, or the time waited"`
	Removed bool   `json:"removed" doc:"It was removed again; if not, remove it by hand"`
}

type tryOutput struct{ Body TryBody }

func registerTry(a huma.API, r *providers.Registry) {
	huma.Register(a, huma.Operation{
		OperationID: "providers-try", Method: http.MethodPost, Path: "/providers/{id}/try",
		Summary: "Try a solver of DNS on a domain: add the record of a challenge, wait for it, and remove it",
		Description: "For solvers whose challenge is dns, but those whose records people add. The record is a TXT record at _acme-challenge under the domain, with a random value. " +
			"A provider that fails to add it answers 502 with its own message.",
	}, func(ctx context.Context, in *tryInput) (*tryOutput, error) {
		return try(ctx, r, in.ID, in.Body.Domain)
	})
}

func try(ctx context.Context, r *providers.Registry, id int64, domain string) (*tryOutput, error) {
	scope := r.Scope("kernel")
	t, err := scope.Type(ctx, id)
	if err != nil {
		return nil, apiError(err)
	}
	if t.Kind != providers.Solver || t.Challenge != "" && t.Challenge != providers.ChallengeDNS || t.Manual {
		return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorProviderUntestable, nil)
	}
	name, ok := tryDomain(domain)
	if !ok {
		return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil).
			WithField("body.domain", i18n.KernelErrorInvalidField)
	}
	p, _, err := scope.Open(ctx, id)
	if err != nil {
		return nil, testError(err)
	}
	solver, ok := p.(contracts.Solver)
	if !ok {
		if c, ok := p.(interface{ Close() error }); ok {
			_ = c.Close()
		}
		return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorProviderUntestable, nil)
	}
	defer func() { _ = solver.Close() }()

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	ch := contracts.Challenge{Domain: name, Token: base64.RawURLEncoding.EncodeToString(b[:16]), KeyAuth: base64.RawURLEncoding.EncodeToString(b)}
	sum := sha256.Sum256([]byte(ch.KeyAuth))
	out := &tryOutput{Body: TryBody{Record: "_acme-challenge." + name, Value: base64.RawURLEncoding.EncodeToString(sum[:])}}
	start := time.Now()

	pctx, cancel := context.WithTimeout(ctx, tryPresent)
	err = solver.Present(pctx, ch)
	cancel()
	if err != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tryCleanUp)
		_ = solver.CleanUp(cctx, ch)
		cancel()
		// The provider's own message, or what it said, as it said it.
		var p *contracts.Problem
		if errors.As(err, &p) {
			return nil, testError(err)
		}
		msg := err.Error()
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		return nil, api.NewError(http.StatusBadGateway, i18n.KernelErrorProviderTry, map[string]any{"message": msg})
	}

	vctx, cancel := context.WithTimeout(ctx, tryVisible)
	// The provider follows a CNAME to put the record, as an issuance does.
	at := out.Body.Record
	if out.Body.Via = delegated(vctx, name); out.Body.Via != "" {
		at = out.Body.Via
	}
	for {
		if seen, err := visible(vctx, at, "TXT", out.Body.Value); err == nil && seen {
			out.Body.Visible = true
			break
		}
		select {
		case <-vctx.Done():
		case <-time.After(tryLook):
			continue
		}
		break
	}
	cancel()
	out.Body.Seconds = int(time.Since(start).Round(time.Second).Seconds())

	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tryCleanUp)
	out.Body.Removed = solver.CleanUp(cctx, ch) == nil
	cancel()
	return out, nil
}

// tryDomain is the domain a trial puts its record under: a wildcard's
// parent, in ASCII, without a final dot; false for what is no host name.
func tryDomain(s string) (string, bool) {
	s = strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "*."), ".")
	ascii, err := idna.Lookup.ToASCII(s)
	if err != nil || !strings.Contains(ascii, ".") || len(ascii) > 253 {
		return "", false
	}
	return ascii, true
}

// DomainsBody lists the domains the account of a solver of DNS manages.
type DomainsBody struct {
	Domains []string `json:"domains" doc:"The first of them, as the provider lists them"`
	Total   int      `json:"total" doc:"How many there are"`
}

type domainsOutput struct{ Body DomainsBody }

func registerDomains(a huma.API, r *providers.Registry) {
	huma.Register(a, huma.Operation{
		OperationID: "providers-domains", Method: http.MethodGet, Path: "/providers/{id}/domains",
		Summary:     "List the domains the account of a solver of DNS manages",
		Description: "For types marked zones: the first ones, for people to pick from. A provider that fails answers 502 with its own message.",
	}, func(ctx context.Context, in *idInput) (*domainsOutput, error) {
		scope := r.Scope("kernel")
		t, err := scope.Type(ctx, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if !t.Zones {
			return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
		}
		p, _, err := scope.Open(ctx, in.ID)
		if err != nil {
			return nil, testError(err)
		}
		if c, ok := p.(interface{ Close() error }); ok {
			defer func() { _ = c.Close() }()
		}
		lister, ok := p.(contracts.DomainLister)
		if !ok {
			return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
		}
		lctx, cancel := context.WithTimeout(ctx, testTimeout)
		defer cancel()
		names, total, err := lister.Domains(lctx)
		if err != nil {
			var p *contracts.Problem
			if errors.As(err, &p) {
				return nil, testError(err)
			}
			return nil, api.NewError(http.StatusBadGateway, i18n.KernelErrorProviderTry, map[string]any{"message": err.Error()})
		}
		if names == nil {
			names = []string{}
		}
		return &domainsOutput{Body: DomainsBody{Domains: names, Total: total}}, nil
	})
}

type preflightInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Domains []string `json:"domains" minItems:"1" maxItems:"100" doc:"The domains of a certificate to be, a wildcard as *.example.com"`
	}
}

// PreflightBody is what a source sees wrong with domains before issuing.
type PreflightBody struct {
	Problems []contracts.Message `json:"problems" doc:"What would make the CA refuse some of the domains, as their CAA records; empty when nothing is seen"`
}

type preflightOutput struct{ Body PreflightBody }

func registerPreflight(a huma.API, r *providers.Registry) {
	huma.Register(a, huma.Operation{
		OperationID: "providers-preflight", Method: http.MethodPost, Path: "/providers/{id}/preflight",
		Summary:     "Tell what a source sees wrong with domains before issuing them",
		Description: "For sources that can tell, as ACME accounts through CAA records; others see nothing. It changes nothing.",
	}, func(ctx context.Context, in *preflightInput) (*preflightOutput, error) {
		scope := r.Scope("kernel")
		t, err := scope.Type(ctx, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		out := &preflightOutput{Body: PreflightBody{Problems: []contracts.Message{}}}
		if t.Kind != providers.Source {
			return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
		}
		p, _, err := scope.Open(ctx, in.ID)
		if err != nil {
			return nil, testError(err)
		}
		if c, ok := p.(interface{ Close() error }); ok {
			defer func() { _ = c.Close() }()
		}
		pf, ok := p.(contracts.Preflighter)
		if !ok {
			return out, nil
		}
		pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		problems, err := pf.Preflight(pctx, in.Body.Domains)
		if err != nil {
			return out, nil // nothing told: the issuance finds out
		}
		out.Body.Problems = append(out.Body.Problems, problems...)
		return out, nil
	})
}

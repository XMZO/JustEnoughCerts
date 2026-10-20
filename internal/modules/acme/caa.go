package acme

import (
	"context"
	"net/url"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

// caaIDs are the domains that CAA records name the CAs by, by the domain
// of their ACME directory. The CAs not here are not checked.
var caaIDs = map[string][]string{
	"letsencrypt.org": {"letsencrypt.org"},
	"zerossl.com":     {"sectigo.com", "zerossl.com"},
	"pki.goog":        {"pki.goog"},
	"ssl.com":         {"ssl.com"},
	"sectigo.com":     {"sectigo.com", "comodoca.com"},
	"digicert.com":    {"digicert.com", "symantec.com", "geotrust.com", "rapidssl.com", "thawte.com", "digitalcertvalidation.com"},
	"globalsign.com":  {"globalsign.com"},
	"actalis.it":      {"actalis.it"},
	"buypass.com":     {"buypass.com"},
	"harica.gr":       {"harica.gr"},
	"trustasia.com":   {"trustasia.com"},
}

// caaOf returns the CAA domains of the CA at directory, nil for one JEC
// does not know.
func caaOf(directory string) []string {
	u, err := url.Parse(directory)
	if err != nil {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	for domain, ids := range caaIDs {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return ids
		}
	}
	return nil
}

// caaProblem tells why the CAA records p of domain d keep the CA known by
// ids from issuing, or nil when they let it.
func caaProblem(p dnscheck.CAAPolicy, ids []string, d string) *contracts.Problem {
	wildcard := strings.HasPrefix(d, "*.")
	if p.Allows(ids, wildcard) {
		return nil
	}
	allowed := p.Issue
	if wildcard && p.Wild {
		allowed = p.IssueWild
	}
	var names []string
	for _, a := range allowed {
		if a != "" {
			names = append(names, a)
		}
	}
	if len(names) == 0 {
		return &contracts.Problem{Key: i18n.AcmeErrorCaaNone, Params: map[string]any{"domain": d, "ca": ids[0]}}
	}
	return &contracts.Problem{Key: i18n.AcmeErrorCaa, Params: map[string]any{"domain": d, "allowed": strings.Join(names, ", "), "ca": ids[0]}}
}

// preflightCAA tells, of each of domains, why its CAA records keep the CA
// at directory from issuing; a domain whose records cannot be read is left
// to the CA.
func preflightCAA(ctx context.Context, lookup func(context.Context, string) (dnscheck.CAAPolicy, error), directory string, domains []string) []contracts.Message {
	ids := caaOf(directory)
	out := []contracts.Message{}
	if ids == nil {
		return out
	}
	for _, d := range domains {
		p, err := lookup(ctx, d)
		if err != nil {
			continue
		}
		if problem := caaProblem(p, ids, d); problem != nil {
			out = append(out, contracts.Message{Key: problem.Key, Params: problem.Params})
		}
	}
	return out
}

// checkCAA makes sure the CAA records of each domain let the CA at
// directory issue, before an order the CA would refuse for them: a policy
// JEC cannot tell, as when DNS does not answer, is left to the CA.
func checkCAA(ctx context.Context, lookup func(context.Context, string) (dnscheck.CAAPolicy, error), directory string, domains []string, log func(contracts.LogLine)) error {
	ids := caaOf(directory)
	if ids == nil {
		return nil
	}
	for _, d := range domains {
		p, err := lookup(ctx, d)
		if err != nil {
			log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AcmeLogCaaUnknown, Params: map[string]any{"domain": d}, Detail: err.Error()})
			continue
		}
		if problem := caaProblem(p, ids, d); problem != nil {
			return retry.Permanent(problem)
		}
	}
	return nil
}

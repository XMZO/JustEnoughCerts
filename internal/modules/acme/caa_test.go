package acme

import (
	"context"
	"errors"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

func TestCheckCAA(t *testing.T) {
	policies := map[string]dnscheck.CAAPolicy{
		"le.example.com":     {Found: true, Issue: []string{"letsencrypt.org"}},
		"google.example.com": {Found: true, Issue: []string{"pki.goog"}},
		"none.example.com":   {Found: true, Issue: []string{""}},
	}
	lookup := func(_ context.Context, d string) (dnscheck.CAAPolicy, error) {
		if d == "broken.example.com" {
			return dnscheck.CAAPolicy{}, errors.New("SERVFAIL")
		}
		return policies[d], nil
	}
	const le = "https://acme-v02.api.letsencrypt.org/directory"
	var logged []contracts.LogLine
	log := func(l contracts.LogLine) { logged = append(logged, l) }

	if err := checkCAA(t.Context(), lookup, le, []string{"le.example.com", "free.example.com", "broken.example.com"}, log); err != nil {
		t.Errorf("allowed: %v", err)
	}
	if len(logged) != 1 || logged[0].Key != i18n.AcmeLogCaaUnknown {
		t.Errorf("logged %+v", logged)
	}
	err := checkCAA(t.Context(), lookup, le, []string{"le.example.com", "google.example.com"}, log)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.AcmeErrorCaa || p.Params["domain"] != "google.example.com" || p.Params["allowed"] != "pki.goog" ||
		p.Params["ca"] != "letsencrypt.org" || !retry.IsPermanent(err) {
		t.Errorf("refused: %v", err)
	}
	if err := checkCAA(t.Context(), lookup, le, []string{"none.example.com"}, log); !errors.As(err, &p) || p.Key != i18n.AcmeErrorCaaNone {
		t.Errorf("none allowed: %v", err)
	}
	// ZeroSSL issues as Sectigo; a CA JEC does not know is left alone.
	if ids := caaOf("https://acme.zerossl.com/v2/DV90"); len(ids) == 0 || ids[0] != "sectigo.com" {
		t.Errorf("zerossl: %v", ids)
	}
	if err := checkCAA(t.Context(), lookup, "https://localhost:14000/dir", []string{"google.example.com"}, log); err != nil {
		t.Errorf("unknown CA: %v", err)
	}
}

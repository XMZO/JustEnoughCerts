package acme

import (
	"context"
	"errors"
	"slices"
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

func TestPreflightCAA(t *testing.T) {
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
	got := preflightCAA(t.Context(), lookup, le, []string{"le.example.com", "google.example.com", "broken.example.com", "none.example.com", "free.example.com"})
	if len(got) != 2 || got[0].Key != i18n.AcmeErrorCaa || got[0].Params["domain"] != "google.example.com" || got[1].Key != i18n.AcmeErrorCaaNone {
		t.Errorf("problems %+v", got)
	}
	// A CA JEC does not know: nothing to say.
	if got := preflightCAA(t.Context(), lookup, "https://pebble:14000/dir", []string{"google.example.com"}); len(got) != 0 {
		t.Errorf("an unknown CA: %+v", got)
	}
}

func TestPreflightNames(t *testing.T) {
	const le = "https://acme-v02.api.letsencrypt.org/directory"
	got := preflightNames(le, []string{"example.com", "*.example.com", "blog.github.io", "nas.lan", "localhost", "192.168.1.10", "203.0.113.7", "2001:db8::1", "::1"})
	var told []string
	for _, m := range got {
		if m.Key != i18n.AcmePreflightInternal {
			t.Errorf("message %+v", m)
		}
		told = append(told, m.Params["domain"].(string))
	}
	// 203.0.113.7 and 2001:db8::1 are for documentation, yet global by
	// their form: the CA has the last word on them.
	if want := []string{"nas.lan", "localhost", "192.168.1.10", "::1"}; !slices.Equal(told, want) {
		t.Errorf("told of %v, want %v", told, want)
	}
	// A CA of one's own may issue for them.
	if got := preflightNames("https://ca.internal:9000/acme/acme/directory", []string{"nas.lan"}); len(got) != 0 {
		t.Errorf("a CA JEC does not know: %+v", got)
	}
}

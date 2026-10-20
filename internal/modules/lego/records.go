package lego

import (
	"context"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnsrecord"
)

// records is the solver of a provider JEC adds records of any type through
// itself, in this process (lib/dnsrecord), with the instance's settings by
// lego's names of them: a contracts.RecordSolver too.
type records struct {
	*solver
	settings map[string]string
}

var (
	_ contracts.RecordSolver = (*records)(nil)
	_ contracts.Tester       = (*records)(nil)
	_ contracts.ZoneHolder   = (*records)(nil)
	_ contracts.DomainLister = (*records)(nil)
)

// Domains lists the first domains the provider manages for the keys.
func (s *records) Domains(ctx context.Context) ([]string, int, error) {
	api, err := dnsrecord.Open(s.code, s.settings)
	if err != nil {
		return nil, 0, err
	}
	return api.Domains(ctx)
}

// ZoneOf finds the zone of name in the DNS and asks the provider whether
// the account holds it.
func (s *records) ZoneOf(ctx context.Context, name string) (string, bool, error) {
	api, err := dnsrecord.Open(s.code, s.settings)
	if err != nil {
		return "", false, err
	}
	return dnsrecord.ZoneOf(ctx, api, name)
}

// shown is how many of the domains a test names.
const shown = 5

// Test lists the domains the provider manages for the keys, which changes
// nothing.
func (s *records) Test(ctx context.Context) (contracts.Message, error) {
	api, err := dnsrecord.Open(s.code, s.settings)
	if err != nil {
		return contracts.Message{}, &contracts.Problem{Key: i18n.LegoErrorTestSettings, Err: err}
	}
	names, total, err := api.Domains(ctx)
	if err != nil {
		return contracts.Message{}, &contracts.Problem{Key: i18n.LegoErrorTest, Params: map[string]any{"message": err.Error()}, Err: err}
	}
	if total == 0 {
		return contracts.Message{Key: i18n.LegoTestNone}, nil
	}
	return contracts.Message{Key: i18n.LegoTestDomains, Params: map[string]any{"count": total, "domains": strings.Join(names[:min(len(names), shown)], ", ")}}, nil
}

// AddRecord adds r and returns the record's ID at the provider.
func (s *records) AddRecord(ctx context.Context, r contracts.Record) (string, error) {
	api, err := dnsrecord.Open(s.code, s.settings)
	if err != nil {
		return "", err
	}
	return api.Add(ctx, dnsrecord.Record{Name: r.Name, Type: r.Type, Value: r.Value})
}

// RemoveRecord removes the record AddRecord added as ref.
func (s *records) RemoveRecord(ctx context.Context, r contracts.Record, ref string) error {
	api, err := dnsrecord.Open(s.code, s.settings)
	if err != nil {
		return err
	}
	return api.Remove(ctx, dnsrecord.Record{Name: r.Name, Type: r.Type, Value: r.Value}, ref)
}

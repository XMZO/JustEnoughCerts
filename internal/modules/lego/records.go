package lego

import (
	"context"

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

var _ contracts.RecordSolver = (*records)(nil)

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

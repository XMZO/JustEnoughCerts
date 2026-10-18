package manual

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// How long JEC looks for the records: the minutes set, within what an
// attempt of an issuance may take.
const (
	defaultMinutes = 20
	maxMinutes     = 25
	interval       = 10 * time.Second
)

func dnsType() providers.Type {
	return providers.Type{
		ID: "manual.dns", Kind: providers.Solver, Challenge: providers.ChallengeDNS,
		Name: i18n.ManualDnsName, Description: i18n.ManualDnsDescription,
		Fields: []providers.Field{
			{Key: "minutes", Label: i18n.ManualFieldMinutes, Help: i18n.ManualFieldMinutesHelp, Kind: providers.Number, Default: strconv.Itoa(defaultMinutes)},
		},
	}
}

// solver waits for records people add: it adds and removes nothing.
type solver struct{ wait time.Duration }

var _ contracts.Solver = (*solver)(nil)

func factory(_ context.Context, in providers.Instance) (any, error) {
	minutes, err := strconv.Atoi(strings.TrimSpace(in.Config["minutes"]))
	if err != nil || minutes <= 0 {
		minutes = defaultMinutes
	}
	return &solver{wait: time.Duration(min(minutes, maxMinutes)) * time.Minute}, nil
}

// Present adds nothing: the issuance's log lists the record, for people to
// add it.
func (*solver) Present(context.Context, contracts.Challenge) error { return nil }

// CleanUp removes nothing: people remove the record when they like.
func (*solver) CleanUp(context.Context, contracts.Challenge) error { return nil }

// Wait is the minutes set, looking every ten seconds.
func (s *solver) Wait() (time.Duration, time.Duration) { return s.wait, interval }

// Close has nothing to release.
func (*solver) Close() error { return nil }

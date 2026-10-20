package providersapi

import (
	"context"
	"errors"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// CheckAll is the kernel's daily check of the providers: each instance of
// a testable type, but notifiers, whose test sends a note, is tested as a
// click on its card would, and keeps what its test said. One that fails
// now, having passed or not been tested since its settings changed, is
// told of (contracts.ProviderFailing): before an issuance or a deployment
// finds a key that stopped working.
func CheckAll(ctx context.Context, r *providers.Registry, tell func(context.Context, contracts.ProviderEvent) error) error {
	list, err := r.List(ctx, "")
	if err != nil {
		return err
	}
	var errs []error
	for _, s := range list {
		if s.Kind == "" || s.Kind == providers.Notifier {
			continue
		}
		if t, ok := r.Type(s.Type); !ok || !t.Testable {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		before := s.LastTest
		_, err := test(ctx, r, s.ID)
		if err == nil || before != nil && !before.OK {
			continue
		}
		ev := contracts.ProviderEvent{ProviderID: s.ID, Name: s.Name, Kind: string(s.Kind), Type: s.Type,
			Problem: contracts.Message{Key: i18n.KernelErrorProviderTest}}
		var e *api.Error
		if errors.As(err, &e) {
			ev.Problem = contracts.Message{Key: e.Code, Params: e.Params}
		}
		if err := tell(ctx, ev); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

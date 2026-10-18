package configfile

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// providerEntry is a provider instance in a file. Certificates and the
// like name it by its name, which is its own among those of its kind.
type providerEntry struct {
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"`
	Config  map[string]string `yaml:"config,omitempty"`
	Secrets map[string]string `yaml:"secrets,omitempty"`
}

// maxName bounds a provider's name, as the API does.
const maxName = 64

// kindOrder lists the kinds as the pages do; types that are not available
// come last.
func kindOrder(k providers.Kind) int {
	i := slices.Index([]providers.Kind{providers.Source, providers.Solver, providers.Target, providers.Notifier}, k)
	if i < 0 {
		return 4
	}
	return i
}

// exportProviders lists the provider instances by kind and name, with
// their secrets if asked for.
func (r *Registry) exportProviders(ctx context.Context, x Export) ([]providerEntry, error) {
	list, err := r.providers.List(ctx, "")
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(list, func(a, b providers.Summary) int { return cmp.Compare(kindOrder(a.Kind), kindOrder(b.Kind)) })
	out := make([]providerEntry, 0, len(list))
	for _, p := range list {
		e := providerEntry{Name: p.Name, Type: p.Type, Config: p.Config}
		if x.Secrets && len(p.SecretKeys) > 0 {
			in, err := r.providers.Load(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			e.Secrets = in.Secrets
		}
		out = append(out, e)
	}
	return out, nil
}

// importProviders adds the provider instances of entries that JEC lacks,
// and changes those it has, by kind and name. Secrets the file leaves out
// stay as they are. Whatever the file has, the names of the instances,
// as it leaves them, go to in for the sections after it.
func (r *Registry) importProviders(ctx context.Context, tx *sql.Tx, entries *[]providerEntry, in *Import) error {
	have, err := r.providers.List(ctx, "")
	if err != nil {
		return err
	}
	in.named = map[ref][]Provider{}
	for _, p := range have {
		if t, ok := r.providers.Type(p.Type); ok {
			key := ref{t.Kind, p.Name}
			in.named[key] = append(in.named[key], Provider{ID: p.ID, Name: p.Name, Type: t})
		}
	}
	if entries == nil {
		return nil
	}
	in.begin(keyProviders, i18n.KernelConfigSectionProviders)
	seen := map[ref]bool{}
	for _, e := range *entries {
		it, err := r.importProvider(ctx, tx, e, in, seen)
		if err != nil {
			return err
		}
		in.Report(it)
	}
	return nil
}

// importProvider adds or changes the instance of e, and says what it did.
func (r *Registry) importProvider(ctx context.Context, tx *sql.Tx, e providerEntry, in *Import, seen map[ref]bool) (Item, error) {
	name := strings.TrimSpace(e.Name)
	it := Item{Name: name, Action: Skip}
	if name == "" || utf8.RuneCountInString(name) > maxName {
		it.Problems = append(it.Problems, Problem{Field: "name", Key: i18n.KernelErrorInvalidField})
		return it, nil
	}
	t, ok := r.providers.Type(e.Type)
	if !ok {
		it.Problems = append(it.Problems, Problem{Field: "type", Key: i18n.KernelConfigErrorType, Params: map[string]any{"type": e.Type}})
		return it, nil
	}
	key := ref{t.Kind, name}
	if seen[key] {
		it.Problems = append(it.Problems, Problem{Field: "name", Key: i18n.KernelConfigErrorDuplicate, Params: map[string]any{"name": name}})
		return it, nil
	}
	seen[key] = true
	found := in.named[key]
	if len(found) > 1 {
		it.Problems = append(it.Problems, Problem{Field: "name", Key: i18n.KernelConfigErrorAmbiguous, Params: map[string]any{"name": name}})
		return it, nil
	}
	if len(found) == 0 {
		id, err := r.providers.CreateTx(ctx, tx, t.ID, name, e.Config, e.Secrets)
		if problems, ok := fieldProblems(err); ok {
			it.Problems = problems
			return it, nil
		}
		if err != nil {
			return it, err
		}
		in.named[key] = []Provider{{ID: id, Name: name, Type: t}}
		it.Action = Add
		return it, nil
	}
	p := found[0]
	if p.Type.ID != t.ID {
		it.Problems = append(it.Problems, Problem{Field: "type", Key: i18n.KernelConfigErrorOtherType, Params: map[string]any{"name": name, "type": p.Type.ID}})
		return it, nil
	}
	had, err := r.providers.Load(ctx, p.ID)
	if err != nil {
		return it, err
	}
	// The secrets the file has replace those JEC has; the others stay.
	secrets := maps.Clone(had.Secrets)
	for k, v := range e.Secrets {
		if strings.TrimSpace(v) != "" {
			secrets[k] = v
		}
	}
	config, secrets, err := r.providers.Normalize(t.ID, e.Config, secrets)
	if problems, ok := fieldProblems(err); ok {
		it.Problems = problems
		return it, nil
	}
	if err != nil {
		return it, err
	}
	if maps.Equal(config, had.Config) && maps.Equal(secrets, had.Secrets) {
		it.Action = Keep
		return it, nil
	}
	if err := r.providers.UpdateTx(ctx, tx, p.ID, nil, config, secrets); err != nil {
		return it, err
	}
	it.Action = Change
	return it, nil
}

// fieldProblems are the problems a provider's settings have, from a
// providers.ValidationError, with their place in the file's entry.
func fieldProblems(err error) ([]Problem, bool) {
	var v *providers.ValidationError
	if !errors.As(err, &v) {
		return nil, false
	}
	out := make([]Problem, 0, len(v.Fields))
	for _, f := range v.Fields {
		out = append(out, Problem{Field: strings.TrimPrefix(f.Location, "body."), Key: f.Code})
	}
	return out, true
}

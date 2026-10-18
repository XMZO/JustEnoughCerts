package configfile

import (
	"context"
	"database/sql"
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

// credentialEntry is a credential in a file. Provider instances name it
// by its name, which is its own.
type credentialEntry struct {
	Name    string            `yaml:"name"`
	Kind    string            `yaml:"kind"`
	Config  map[string]string `yaml:"config,omitempty"`
	Secrets map[string]string `yaml:"secrets,omitempty"`
}

// namedCredential is a credential that a file's providers can name.
type namedCredential struct {
	id   int64
	kind string
}

// exportCredentials lists the credentials by name, with their secrets if
// asked for, and returns their names by ID for the providers.
func (r *Registry) exportCredentials(ctx context.Context, x Export) ([]credentialEntry, map[int64]string, error) {
	list, err := r.providers.Credentials(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]credentialEntry, 0, len(list))
	names := map[int64]string{}
	for _, c := range list {
		names[c.ID] = c.Name
		e := credentialEntry{Name: c.Name, Kind: c.Kind, Config: c.Config}
		if x.Secrets && len(c.SecretKeys) > 0 {
			full, err := r.providers.LoadCredential(ctx, c.ID)
			if err != nil {
				return nil, nil, err
			}
			e.Secrets = full.Secrets
		}
		out = append(out, e)
	}
	return out, names, nil
}

// importCredentials adds the credentials of entries that JEC lacks, and
// changes those it has, by name. Secrets the file leaves out stay as they
// are. Whatever the file has, the credentials, as it leaves them, go to in
// for the providers that name them.
func (r *Registry) importCredentials(ctx context.Context, tx *sql.Tx, entries *[]credentialEntry, in *Import) error {
	have, err := r.providers.Credentials(ctx)
	if err != nil {
		return err
	}
	in.credentials = map[string]namedCredential{}
	for _, c := range have {
		in.credentials[c.Name] = namedCredential{c.ID, c.Kind}
	}
	if entries == nil {
		return nil
	}
	in.begin(keyCredentials, i18n.KernelConfigSectionCredentials)
	seen := map[string]bool{}
	for _, e := range *entries {
		it, err := r.importCredential(ctx, tx, e, in, seen)
		if err != nil {
			return err
		}
		in.Report(it)
	}
	return nil
}

// importCredential adds or changes the credential of e, and says what it
// did.
func (r *Registry) importCredential(ctx context.Context, tx *sql.Tx, e credentialEntry, in *Import, seen map[string]bool) (Item, error) {
	name := strings.TrimSpace(e.Name)
	it := Item{Name: name, Action: Skip}
	switch {
	case name == "" || utf8.RuneCountInString(name) > maxName:
		it.Problems = append(it.Problems, Problem{Field: "name", Key: i18n.KernelErrorInvalidField})
		return it, nil
	case seen[name]:
		it.Problems = append(it.Problems, Problem{Field: "name", Key: i18n.KernelConfigErrorDuplicate, Params: map[string]any{"name": name}})
		return it, nil
	}
	seen[name] = true
	if _, ok := r.providers.Kind(e.Kind); !ok {
		it.Problems = append(it.Problems, Problem{Field: "kind", Key: i18n.KernelConfigErrorCredentialKind, Params: map[string]any{"kind": e.Kind}})
		return it, nil
	}
	had, ok := in.credentials[name]
	if !ok {
		id, err := r.providers.CreateCredentialTx(ctx, tx, e.Kind, name, e.Config, e.Secrets)
		if problems, ok := fieldProblems(err); ok {
			it.Problems = problems
			return it, nil
		}
		if err != nil {
			return it, err
		}
		in.credentials[name] = namedCredential{id, e.Kind}
		it.Action = Add
		return it, nil
	}
	if had.kind != e.Kind {
		it.Problems = append(it.Problems, Problem{Field: "kind", Key: i18n.KernelConfigErrorCredentialOther, Params: map[string]any{"name": name, "kind": had.kind}})
		return it, nil
	}
	old, err := r.providers.LoadCredential(ctx, had.id)
	if err != nil {
		return it, err
	}
	// The secrets the file has replace those JEC has; the others stay.
	secrets := map[string]string{}
	for k, v := range e.Secrets {
		if strings.TrimSpace(v) != "" {
			secrets[k] = v
		}
	}
	config := e.Config
	if config == nil {
		config = map[string]string{}
	}
	merged := maps.Clone(old.Secrets)
	maps.Copy(merged, secrets)
	if maps.Equal(config, old.Config) && maps.Equal(merged, old.Secrets) {
		it.Action = Keep
		return it, nil
	}
	err = r.providers.UpdateCredentialTx(ctx, tx, had.id, nil, config, secrets)
	if problems, ok := fieldProblems(err); ok {
		it.Problems = problems
		return it, nil
	}
	if err != nil {
		return it, err
	}
	it.Action = Change
	return it, nil
}

// Package providersapi serves the provider registry under /api/v1/providers:
// the types the modules offer, with the fields their forms are built from,
// and the configured instances; and under /api/v1/credentials, the
// credentials that instances share.
package providersapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// TypeSummary is a type as listed, without its fields.
type TypeSummary struct {
	ID           string         `json:"id"`
	Kind         providers.Kind `json:"kind" enum:"source,solver,target,notifier"`
	Module       string         `json:"module"`
	Name         i18n.Key       `json:"name,omitempty" doc:"Message key of the name, for types JEC describes"`
	Description  i18n.Key       `json:"description,omitempty"`
	Brand        string         `json:"brand,omitempty" doc:"A third party's name for the type, shown as it is"`
	URL          string         `json:"url,omitempty"`
	Tags         []string       `json:"tags,omitempty" doc:"The groups of the type in the UI; a solver that adds records of any type is in records too"`
	NeedsSolver  bool           `json:"needsSolver,omitempty" doc:"For a source: issuing needs a solver"`
	OwnKey       bool           `json:"ownKey,omitempty" doc:"For a source: its certificates come with their own private key, so there is no key type to choose"`
	Revocable    bool           `json:"revocable,omitempty" doc:"For a source: it can have the certificates it issued revoked"`
	Challenges   []string       `json:"challenges,omitempty" enum:"dns,http,record" doc:"For a source that needs a solver: the challenges of the solvers it can use; record takes a solver of DNS that adds records of any type"`
	Challenge    string         `json:"challenge,omitempty" enum:"dns,http" doc:"For a solver: how it proves control of a domain, through DNS records or files on the web server"`
	Records      bool           `json:"records,omitempty" doc:"For a solver of DNS: it adds records of any type too, as some sources ask"`
	Testable     bool           `json:"testable,omitempty" doc:"Its instances can be tested"`
	Experimental bool           `json:"experimental,omitempty"`
	Untested     bool           `json:"untested,omitempty" doc:"Tested only against a stand-in for the service's API, never with a real account: it may not work"`
	// CredentialKind is the kind of credential the type can take some of
	// its fields from.
	CredentialKind string `json:"credentialKind,omitempty" doc:"The kind of credential the type can take some of its fields from"`
	// Texts holds the messages of the keys above that the locale files
	// lack, such as a plugin's.
	Texts map[string]map[i18n.Key]string `json:"texts,omitempty" doc:"The type's own messages, by language tag and key, for keys the locale files lack, such as a plugin's"`
}

// InstanceBody is an instance as the API returns it: secrets are never
// sent back, only which of them are set.
type InstanceBody struct {
	ID         int64             `json:"id"`
	Type       string            `json:"type"`
	Kind       string            `json:"kind" doc:"The type's kind, or empty when the type is not available"`
	Name       string            `json:"name"`
	Config     map[string]string `json:"config"`
	SecretKeys []string          `json:"secretKeys" doc:"The secret fields that hold a value"`
	Credential int64             `json:"credentialId,omitempty" doc:"The credential that fills the fields the type takes from one"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
	LastTest   *TestBody         `json:"lastTest,omitempty" doc:"What the last test said, until the settings change"`
}

// TestBody is what a test of an instance said, when: its message of
// success, or its error.
type TestBody struct {
	At      time.Time         `json:"at"`
	OK      bool              `json:"ok"`
	Message contracts.Message `json:"message"`
}

func instanceBody(s providers.Summary) InstanceBody {
	b := InstanceBody{
		ID: s.ID, Type: s.Type, Kind: string(s.Kind), Name: s.Name, Config: s.Config,
		SecretKeys: s.SecretKeys, Credential: s.Credential, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
	if t := s.LastTest; t != nil {
		b.LastTest = &TestBody{At: t.At, OK: t.OK, Message: contracts.Message{Key: t.Key, Params: t.Params}}
	}
	return b
}

type kindInput struct {
	Kind string `query:"kind" enum:"source,solver,target,notifier,"`
}

type typesOutput struct {
	Body struct {
		Types []TypeSummary `json:"types"`
	}
}

type typeInput struct {
	ID string `path:"id"`
}

type typeOutput struct {
	Body providers.Type
}

type listOutput struct {
	Body struct {
		Providers []InstanceBody `json:"providers"`
	}
}

type idInput struct {
	ID int64 `path:"id"`
}

type instanceOutput struct {
	Body InstanceBody
}

type testOutput struct {
	Body struct {
		Message contracts.Message `json:"message" doc:"What the test found"`
	}
}

type createInput struct {
	Body struct {
		Type       string            `json:"type" minLength:"1"`
		Name       string            `json:"name" minLength:"1" maxLength:"64"`
		Config     map[string]string `json:"config,omitempty"`
		Secrets    map[string]string `json:"secrets,omitempty"`
		Credential int64             `json:"credentialId,omitempty" doc:"A credential to fill the fields the type takes from one"`
		// NewCredential keeps the values of those fields as a credential.
		NewCredential bool `json:"newCredential,omitempty" doc:"The values given for the fields the type takes from a credential become a new credential, named after the provider, that others can share"`
	}
}

type updateInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Name          *string           `json:"name,omitempty" minLength:"1" maxLength:"64"`
		Config        map[string]string `json:"config,omitempty" doc:"Replaces the plain settings when given"`
		Secrets       map[string]string `json:"secrets,omitempty" doc:"Merged: a value replaces that secret, an empty string removes it"`
		Credential    *int64            `json:"credentialId,omitempty" doc:"Sets the credential that fills the fields the type takes from one; 0 for none, so that they take values of their own"`
		NewCredential bool              `json:"newCredential,omitempty" doc:"The values of the fields the type takes from a credential, those given or else the provider's own, become a new credential, named after the provider"`
	}
}

// Register adds the API of r under /api/v1/providers.
func Register(a huma.API, r *providers.Registry) {
	huma.Register(a, huma.Operation{
		OperationID: "providers-types", Method: http.MethodGet, Path: "/providers/types",
		Summary: "List the provider types the modules offer",
	}, func(_ context.Context, in *kindInput) (*typesOutput, error) {
		out := &typesOutput{}
		out.Body.Types = []TypeSummary{}
		for _, t := range r.Types(providers.Kind(in.Kind)) {
			sum := TypeSummary{
				ID: t.ID, Kind: t.Kind, Module: t.Module, Name: t.Name, Description: t.Description,
				Brand: t.Brand, URL: t.URL, Tags: t.Tags, NeedsSolver: t.NeedsSolver, OwnKey: t.OwnKey, Revocable: t.Revocable, Testable: t.Testable, Experimental: t.Experimental, Untested: t.Untested,
				CredentialKind: t.CredentialKind, Texts: t.Texts,
			}
			switch {
			case t.Kind == providers.Solver:
				sum.Challenge, sum.Records = t.Solves(), t.Records
				if t.Records { // a group of their own in the UI
					sum.Tags = append(slices.Clip(t.Tags), "records")
				}
			case t.NeedsSolver:
				sum.Challenges = []string{}
				for _, c := range []string{providers.ChallengeDNS, providers.ChallengeHTTP, providers.ChallengeRecord} {
					if t.Accepts(c) {
						sum.Challenges = append(sum.Challenges, c)
					}
				}
			}
			out.Body.Types = append(out.Body.Types, sum)
		}
		return out, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "providers-type", Method: http.MethodGet, Path: "/providers/types/{id}",
		Summary: "Get a provider type with the fields of its form",
	}, func(_ context.Context, in *typeInput) (*typeOutput, error) {
		t, ok := r.Type(in.ID)
		if !ok {
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorProviderTypeUnknown, nil)
		}
		return &typeOutput{Body: t}, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "providers-list", Method: http.MethodGet, Path: "/providers",
		Summary: "List the configured providers",
	}, func(ctx context.Context, in *kindInput) (*listOutput, error) {
		list, err := r.List(ctx, providers.Kind(in.Kind))
		if err != nil {
			return nil, err
		}
		out := &listOutput{}
		out.Body.Providers = make([]InstanceBody, 0, len(list))
		for _, s := range list {
			out.Body.Providers = append(out.Body.Providers, instanceBody(s))
		}
		return out, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "providers-create", Method: http.MethodPost, Path: "/providers", DefaultStatus: http.StatusCreated,
		Summary: "Configure a provider",
	}, func(ctx context.Context, in *createInput) (*instanceOutput, error) {
		b := in.Body
		id, err := r.CreateShared(ctx, b.Type, b.Name, b.Config, b.Secrets, b.Credential, b.NewCredential)
		if err != nil {
			return nil, apiError(err)
		}
		return output(ctx, r, id)
	})

	huma.Register(a, huma.Operation{
		OperationID: "providers-get", Method: http.MethodGet, Path: "/providers/{id}",
		Summary: "Get a configured provider",
	}, func(ctx context.Context, in *idInput) (*instanceOutput, error) {
		return output(ctx, r, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "providers-update", Method: http.MethodPatch, Path: "/providers/{id}",
		Summary: "Change a configured provider",
	}, func(ctx context.Context, in *updateInput) (*instanceOutput, error) {
		b := in.Body
		if err := r.UpdateShared(ctx, in.ID, b.Name, b.Config, b.Secrets, b.Credential, b.NewCredential); err != nil {
			return nil, apiError(err)
		}
		return output(ctx, r, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "providers-test", Method: http.MethodPost, Path: "/providers/{id}/test",
		Summary:     "Test a configured provider",
		Description: "Checks its settings without changing anything, for types marked testable. A provider that fails answers 502 with its own message.",
	}, func(ctx context.Context, in *idInput) (*testOutput, error) {
		return test(ctx, r, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "providers-delete", Method: http.MethodDelete, Path: "/providers/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "Delete a configured provider, unless a certificate uses it",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		return nil, apiError(r.Delete(ctx, in.ID))
	})

	registerCredentials(a, r)
}

// testTimeout bounds a test, which may reach a server across the world.
const testTimeout = time.Minute

func test(ctx context.Context, r *providers.Registry, id int64) (*testOutput, error) {
	scope := r.Scope("kernel")
	t, err := scope.Type(ctx, id)
	if err != nil {
		return nil, apiError(err)
	}
	if !t.Testable {
		return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorProviderUntestable, nil)
	}
	p, _, err := scope.Open(ctx, id)
	if err != nil {
		return nil, testError(err)
	}
	if c, ok := p.(interface{ Close() error }); ok {
		defer func() { _ = c.Close() }()
	}
	tester, ok := p.(contracts.Tester)
	if !ok {
		return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorProviderUntestable, nil)
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	msg, err := tester.Test(tctx)
	// What it said shows on the provider's card until its settings change;
	// failing to keep it changes nothing of the answer.
	if err != nil {
		var p *contracts.Problem
		key, params := i18n.KernelErrorProviderTest, map[string]any(nil)
		if errors.As(err, &p) {
			key, params = p.Key, p.Params
		}
		_ = r.RecordTest(ctx, id, false, key, params)
		return nil, testError(err)
	}
	_ = r.RecordTest(ctx, id, true, msg.Key, msg.Params)
	out := &testOutput{}
	out.Body.Message = msg
	return out, nil
}

// testError turns a failed test into a response: the provider's own
// message where it has one.
func testError(err error) error {
	var p *contracts.Problem
	switch {
	case errors.As(err, &p):
		return api.NewError(http.StatusBadGateway, p.Key, p.Params)
	case errors.Is(err, providers.ErrNotFound), errors.Is(err, providers.ErrUnavailable):
		return apiError(err)
	}
	return api.NewError(http.StatusBadGateway, i18n.KernelErrorProviderTest, nil)
}

func output(ctx context.Context, r *providers.Registry, id int64) (*instanceOutput, error) {
	s, err := r.Get(ctx, id)
	if err != nil {
		return nil, apiError(err)
	}
	return &instanceOutput{Body: instanceBody(s)}, nil
}

// apiError turns the registry's errors into responses.
func apiError(err error) error {
	var invalid *providers.ValidationError
	var inUse *providers.InUseError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, providers.ErrNotFound):
		return api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
	case errors.Is(err, providers.ErrUnavailable):
		return api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorProviderTypeUnknown, nil).
			WithField("body.type", i18n.KernelErrorProviderTypeUnknown)
	case errors.As(err, &inUse):
		return api.NewError(http.StatusConflict, i18n.KernelErrorProviderInUse, nil)
	case errors.As(err, &invalid):
		e := api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
		for _, f := range invalid.Fields {
			e = e.WithField(f.Location, f.Code)
		}
		return e
	}
	return err
}

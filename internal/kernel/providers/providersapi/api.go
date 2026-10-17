// Package providersapi serves the provider registry under /api/v1/providers:
// the types the modules offer, with the fields their forms are built from,
// and the configured instances.
package providersapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
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
	Tags         []string       `json:"tags,omitempty"`
	Experimental bool           `json:"experimental,omitempty"`
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
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

func instanceBody(s providers.Summary) InstanceBody {
	return InstanceBody{
		ID: s.ID, Type: s.Type, Kind: string(s.Kind), Name: s.Name, Config: s.Config,
		SecretKeys: s.SecretKeys, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
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

type createInput struct {
	Body struct {
		Type    string            `json:"type" minLength:"1"`
		Name    string            `json:"name" minLength:"1" maxLength:"64"`
		Config  map[string]string `json:"config,omitempty"`
		Secrets map[string]string `json:"secrets,omitempty"`
	}
}

type updateInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Name    *string           `json:"name,omitempty" minLength:"1" maxLength:"64"`
		Config  map[string]string `json:"config,omitempty" doc:"Replaces the plain settings when given"`
		Secrets map[string]string `json:"secrets,omitempty" doc:"Merged: a value replaces that secret, an empty string removes it"`
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
			out.Body.Types = append(out.Body.Types, TypeSummary{
				ID: t.ID, Kind: t.Kind, Module: t.Module, Name: t.Name, Description: t.Description,
				Brand: t.Brand, URL: t.URL, Tags: t.Tags, Experimental: t.Experimental,
			})
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
		id, err := r.Create(ctx, in.Body.Type, in.Body.Name, in.Body.Config, in.Body.Secrets)
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
		if err := r.Update(ctx, in.ID, in.Body.Name, in.Body.Config, in.Body.Secrets); err != nil {
			return nil, apiError(err)
		}
		return output(ctx, r, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "providers-delete", Method: http.MethodDelete, Path: "/providers/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "Delete a configured provider, unless a certificate uses it",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		return nil, apiError(r.Delete(ctx, in.ID))
	})
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

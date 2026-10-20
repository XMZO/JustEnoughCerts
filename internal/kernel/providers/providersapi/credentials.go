package providersapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// CredentialUser is an instance that a credential fills the fields of.
type CredentialUser struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Kind string `json:"kind" doc:"The type's kind, or empty when the type is not available"`
}

// CredentialBody is a credential as the API returns it: secrets are never
// sent back, only which of them are set.
type CredentialBody struct {
	ID         int64             `json:"id"`
	Kind       string            `json:"kind"`
	Available  bool              `json:"available" doc:"An available type takes its kind"`
	Name       string            `json:"name"`
	Config     map[string]string `json:"config"`
	SecretKeys []string          `json:"secretKeys" doc:"The secret fields that hold a value"`
	UsedBy     []CredentialUser  `json:"usedBy" doc:"The instances whose fields it fills"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
	UsedAt     *time.Time        `json:"usedAt,omitempty" doc:"When an instance last used it"`
}

func credentialBody(c providers.CredentialSummary) CredentialBody {
	b := CredentialBody{
		ID: c.ID, Kind: c.Kind, Available: c.Available, Name: c.Name, Config: c.Config, SecretKeys: c.SecretKeys,
		UsedBy: make([]CredentialUser, 0, len(c.UsedBy)), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	for _, u := range c.UsedBy {
		b.UsedBy = append(b.UsedBy, CredentialUser{ID: u.ID, Name: u.Name, Type: u.Type, Kind: string(u.Kind)})
	}
	if !c.UsedAt.IsZero() {
		used := c.UsedAt
		b.UsedAt = &used
	}
	return b
}

type kindsOutput struct {
	Body struct {
		Kinds []providers.CredentialKind `json:"kinds"`
	}
}

type credentialsOutput struct {
	Body struct {
		Credentials []CredentialBody `json:"credentials"`
	}
}

type credentialOutput struct {
	Body CredentialBody
}

type createCredentialInput struct {
	Body struct {
		Kind    string            `json:"kind" minLength:"1"`
		Name    string            `json:"name" minLength:"1" maxLength:"64"`
		Config  map[string]string `json:"config,omitempty"`
		Secrets map[string]string `json:"secrets,omitempty"`
	}
}

type updateCredentialInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Name    *string           `json:"name,omitempty" minLength:"1" maxLength:"64"`
		Config  map[string]string `json:"config,omitempty" doc:"Replaces the plain values when given"`
		Secrets map[string]string `json:"secrets,omitempty" doc:"Merged: a value replaces that secret, an empty string removes it"`
	}
}

// registerCredentials adds the API of the credentials under
// /api/v1/credentials.
func registerCredentials(a huma.API, r *providers.Registry) {
	tags := []string{"credentials"}
	huma.Register(a, huma.Operation{
		OperationID: "credentials-kinds", Method: http.MethodGet, Path: "/credentials/kinds", Tags: tags,
		Summary: "List the kinds of credential that the available provider types take",
	}, func(context.Context, *struct{}) (*kindsOutput, error) {
		out := &kindsOutput{}
		out.Body.Kinds = append([]providers.CredentialKind{}, r.Kinds()...)
		return out, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "credentials-list", Method: http.MethodGet, Path: "/credentials", Tags: tags,
		Summary: "List the credentials, with the providers that use each",
	}, func(ctx context.Context, _ *struct{}) (*credentialsOutput, error) {
		list, err := r.Credentials(ctx)
		if err != nil {
			return nil, err
		}
		out := &credentialsOutput{}
		out.Body.Credentials = make([]CredentialBody, 0, len(list))
		for _, c := range list {
			out.Body.Credentials = append(out.Body.Credentials, credentialBody(c))
		}
		return out, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "credentials-create", Method: http.MethodPost, Path: "/credentials", Tags: tags, DefaultStatus: http.StatusCreated,
		Summary: "Add a credential",
	}, func(ctx context.Context, in *createCredentialInput) (*credentialOutput, error) {
		id, err := r.CreateCredential(ctx, in.Body.Kind, in.Body.Name, in.Body.Config, in.Body.Secrets)
		if err != nil {
			return nil, credentialError(err)
		}
		return credentialOut(ctx, r, id)
	})

	huma.Register(a, huma.Operation{
		OperationID: "credentials-get", Method: http.MethodGet, Path: "/credentials/{id}", Tags: tags,
		Summary: "Get a credential",
	}, func(ctx context.Context, in *idInput) (*credentialOutput, error) {
		return credentialOut(ctx, r, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "credentials-update", Method: http.MethodPatch, Path: "/credentials/{id}", Tags: tags,
		Summary: "Change a credential", Description: "The providers that use it take the new values the next time they run.",
	}, func(ctx context.Context, in *updateCredentialInput) (*credentialOutput, error) {
		if err := r.UpdateCredential(ctx, in.ID, in.Body.Name, in.Body.Config, in.Body.Secrets); err != nil {
			return nil, credentialError(err)
		}
		return credentialOut(ctx, r, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "credentials-delete", Method: http.MethodDelete, Path: "/credentials/{id}", Tags: tags, DefaultStatus: http.StatusNoContent,
		Summary: "Delete a credential, unless a provider uses it",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if c, err := r.GetCredential(ctx, in.ID); err == nil {
			api.Subject(ctx, c.Name)
		}
		return nil, credentialError(r.DeleteCredential(ctx, in.ID))
	})
}

func credentialOut(ctx context.Context, r *providers.Registry, id int64) (*credentialOutput, error) {
	c, err := r.GetCredential(ctx, id)
	if err != nil {
		return nil, credentialError(err)
	}
	api.Subject(ctx, c.Name)
	return &credentialOutput{Body: credentialBody(c)}, nil
}

// credentialError turns the registry's errors about credentials into
// responses.
func credentialError(err error) error {
	var inUse *providers.CredentialInUseError
	switch {
	case errors.Is(err, providers.ErrCredentialNotFound):
		return api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
	case errors.Is(err, providers.ErrKindUnavailable):
		return api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorCredentialKind, nil).WithField("body.kind", i18n.KernelErrorCredentialKind)
	case errors.As(err, &inUse):
		return api.NewError(http.StatusConflict, i18n.KernelErrorCredentialInUse, map[string]any{"names": strings.Join(inUse.Names, ", ")})
	}
	return apiError(err)
}

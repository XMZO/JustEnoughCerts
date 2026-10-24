package whoice

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/lookup"
)

// RegistrationBody is what a lookup found of a domain's registration.
type RegistrationBody struct {
	Name      string     `json:"name" doc:"The registered domain looked up, in ASCII"`
	Status    string     `json:"status" enum:"registered,unregistered,reserved,unknown" doc:"What the registry, or else the DNS delegation, tells of the name"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty" doc:"When the registration ends, as the registry told"`
	Registrar string     `json:"registrar,omitempty"`
	Source    string     `json:"source,omitempty" enum:"rdap,whois,whoisWeb,dns" doc:"Where the end, or else the status, came from"`
	Server    string     `json:"server,omitempty" doc:"The server that told it"`
	Note      string     `json:"note,omitempty" doc:"Why there is no end, as the lookup tells it"`
	Reason    string     `json:"reason,omitempty" enum:"no_lookup,no_name,no_end,no_answer" doc:"Why there is no end: no_lookup: the registry has no RDAP or WHOIS to ask from here; no_name: it, or else the DNS delegation, has no such name; no_end: it tells no end; no_answer: none answered as it should"`
	Failed    bool       `json:"failed" doc:"Nothing answered as it should, as opposed to a registry that tells no end: worth asking again soon"`
}

type domainInput struct {
	Name string `path:"name" maxLength:"300" doc:"A domain or a name under it; the registered domain is looked up"`
}

type domainOutput struct{ Body RegistrationBody }

func registerAPI(a huma.API, m *Module) {
	huma.Register(a, huma.Operation{
		OperationID: "whoice-domain", Method: http.MethodGet, Path: "/domains/{name}", Tags: []string{"whoice"},
		Summary: "Look up when a domain's registration ends, through RDAP, WHOIS, registries' web lookups and the DNS delegation",
	}, func(ctx context.Context, in *domainInput) (*domainOutput, error) {
		r, err := m.lookup.Domain(ctx, in.Name)
		if errors.Is(err, lookup.ErrDomain) {
			return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil).WithField("path.name", i18n.WhoiceErrorDomain)
		}
		out := &domainOutput{Body: RegistrationBody{Name: r.Name}}
		if err != nil {
			out.Body.Name, out.Body.Status, out.Body.Note, out.Body.Failed = in.Name, "unknown", err.Error(), true
			return out, nil
		}
		out.Body.Status, out.Body.Registrar, out.Body.Source, out.Body.Server = string(r.Status), r.Registrar, string(r.Source), r.Server
		out.Body.Note, out.Body.Reason, out.Body.Failed = r.Note, r.Reason, r.Failed
		if !r.Expires.IsZero() {
			out.Body.ExpiresAt = &r.Expires
		}
		return out, nil
	})
}

package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

// DomainBody is a domain as the API tells it.
type DomainBody struct {
	Name         string     `json:"name" doc:"The registered domain, as example.com"`
	Manual       bool       `json:"manual" doc:"Added by hand; the others come and go with the certificates and sites under them"`
	Certificates []int64    `json:"certificates" doc:"The certificates with names under the domain"`
	Sites        []int64    `json:"sites" doc:"The sites watched under the domain"`
	State        string     `json:"state" enum:"pending,ok,expiring,expired,unknown,failed" doc:"pending: not looked up yet; expiring: ends within 30 days; unknown: the registry tells no end, or has no RDAP; failed: the last lookup failed, what was told before kept"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty" doc:"When the registration ends, as the registry told"`
	Registrar    string     `json:"registrar,omitempty"`
	Error        string     `json:"error,omitempty" doc:"Why the last lookup told no end, as the lookup said it"`
	Cause        string     `json:"cause,omitempty" enum:"no_lookup,no_name,no_end,no_answer" doc:"Why the end is not known, when the state is unknown: no_lookup: the registry has no RDAP or WHOIS to ask from here; no_name: it has no such name; no_end: it tells no end; no_answer: none answered as it should"`
	CheckedAt    *time.Time `json:"checkedAt,omitempty"`
	CheckAt      time.Time  `json:"checkAt" doc:"When the next lookup comes"`
}

type domainsOutput struct {
	Body struct {
		Domains []DomainBody `json:"domains"`
	}
}

type domainOutput struct{ Body DomainBody }

type addDomainInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"300" doc:"A domain or a name under it, or a URL; the registered domain is watched"`
	}
}

type nameInput struct {
	Name string `path:"name" maxLength:"300"`
}

func registerDomainsAPI(a huma.API, m *Module) {
	tags := []string{"watch"}
	huma.Register(a, huma.Operation{
		OperationID: "watch-domains", Method: http.MethodGet, Path: "/domains", Tags: tags,
		Summary: "List the domains whose registration is watched",
	}, func(ctx context.Context, _ *struct{}) (*domainsOutput, error) {
		list, err := m.domains(ctx, "")
		if err != nil {
			return nil, err
		}
		out := &domainsOutput{}
		out.Body.Domains = list
		return out, nil
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-add-domain", Method: http.MethodPost, Path: "/domains", Tags: tags, DefaultStatus: http.StatusCreated,
		Summary: "Watch the registration of a domain, which is looked up at once",
	}, func(ctx context.Context, in *addDomainInput) (*domainOutput, error) {
		host, _, ok := parseHost(in.Body.Name)
		name := registered(host)
		if !ok || name == "" {
			return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil).WithField("body.name", i18n.WatchErrorDomain)
		}
		api.Subject(ctx, name)
		var n int
		if err := m.db.QueryRowContext(ctx, "SELECT count(*) FROM watch_domains WHERE manual = 1").Scan(&n); err != nil {
			return nil, err
		}
		if n >= maxDomains {
			return nil, api.NewError(http.StatusConflict, i18n.WatchErrorTooManyDomains, map[string]any{"max": maxDomains})
		}
		if err := m.addDomain(ctx, name); err != nil {
			return nil, err
		}
		if err := m.checkDomain(ctx, name); err != nil {
			return nil, err
		}
		return m.oneDomain(ctx, name)
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-delete-domain", Method: http.MethodDelete, Path: "/domains/{name}", Tags: tags, DefaultStatus: http.StatusNoContent,
		Summary:     "Stop watching a domain added by hand",
		Description: "A domain that a certificate or a site is under stays watched.",
	}, func(ctx context.Context, in *nameInput) (*struct{}, error) {
		api.Subject(ctx, in.Name)
		res, err := m.db.ExecContext(ctx, "UPDATE watch_domains SET manual = 0 WHERE name = ? AND manual = 1", in.Name)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		}
		_, err = m.db.ExecContext(ctx, "DELETE FROM watch_domains WHERE name = ? AND manual = 0 AND certificates = '[]' AND sites = '[]'", in.Name)
		return nil, err
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-check-domain", Method: http.MethodPost, Path: "/domains/{name}/check", Tags: tags,
		Summary: "Look up the registration of a domain now",
	}, func(ctx context.Context, in *nameInput) (*domainOutput, error) {
		api.Subject(ctx, in.Name)
		if err := m.checkDomain(ctx, in.Name); errors.Is(err, errNotFound) {
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		} else if err != nil {
			return nil, err
		}
		return m.oneDomain(ctx, in.Name)
	})
}

// addDomain marks domain name as added by hand, adding it when no
// certificate or site is under it.
func (m *Module) addDomain(ctx context.Context, name string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var manual int64
	err = tx.QueryRowContext(ctx, "SELECT manual FROM watch_domains WHERE name = ?", name).Scan(&manual)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		now := m.now().UnixMilli()
		_, err = tx.ExecContext(ctx,
			`INSERT INTO watch_domains (name, manual, certificates, sites, created_at, check_at, state, registrar, error, notified)
			 VALUES (?, 1, '[]', '[]', ?, ?, ?, '', '', '')`, name, now, now, statePending)
	case err != nil:
	case manual == 1:
		return api.NewError(http.StatusConflict, i18n.KernelErrorInvalidRequest, nil).WithField("body.name", i18n.WatchErrorDomainWatched)
	default:
		_, err = tx.ExecContext(ctx, "UPDATE watch_domains SET manual = 1 WHERE name = ?", name)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Module) oneDomain(ctx context.Context, name string) (*domainOutput, error) {
	list, err := m.domains(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
	}
	return &domainOutput{Body: list[0]}, nil
}

// domains returns the domains by name, or only name when not "". They are
// sorted here, as databases order text by different rules.
func (m *Module) domains(ctx context.Context, name string) ([]DomainBody, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT name, manual, certificates, sites, state, expires_at, registrar, error, cause, checked_at, check_at
		 FROM watch_domains WHERE ? = '' OR name = ?`, name, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DomainBody{}
	for rows.Next() {
		var d DomainBody
		var manual, checkAt int64
		var certs, sites string
		var expires, checked sql.NullInt64
		if err := rows.Scan(&d.Name, &manual, &certs, &sites, &d.State, &expires, &d.Registrar, &d.Error, &d.Cause, &checked, &checkAt); err != nil {
			return nil, err
		}
		d.Manual = manual == 1
		if json.Unmarshal([]byte(certs), &d.Certificates) != nil || d.Certificates == nil {
			d.Certificates = []int64{}
		}
		if json.Unmarshal([]byte(sites), &d.Sites) != nil || d.Sites == nil {
			d.Sites = []int64{}
		}
		if expires.Valid {
			t := time.UnixMilli(expires.Int64).UTC()
			d.ExpiresAt = &t
		}
		if checked.Valid {
			t := time.UnixMilli(checked.Int64).UTC()
			d.CheckedAt = &t
		}
		d.CheckAt = time.UnixMilli(checkAt).UTC()
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b DomainBody) int { return strings.Compare(a.Name, b.Name) })
	return out, rows.Err()
}

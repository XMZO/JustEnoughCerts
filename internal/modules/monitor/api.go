package monitor

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

// DomainCheckBody is what a domain served the last time it was checked.
type DomainCheckBody struct {
	Domain      string     `json:"domain"`
	View        string     `json:"view,omitempty" enum:"cn,global" doc:"cn: as visitors from mainland China reach the domain; global: as those from elsewhere do; absent when its DNS sends them the same way"`
	State       string     `json:"state" enum:"ok,old,other,unreachable" doc:"ok: the version in use; old: an older certificate for the domain from the same CA; other: someone else's certificate, such as a CDN's own, or one for other names, or a newer one; unreachable: nothing over TLS on port 443"`
	Fingerprint string     `json:"fingerprint,omitempty" doc:"SHA-256 of the certificate served, lowercase hex"`
	NotAfter    *time.Time `json:"notAfter,omitempty" doc:"When the certificate served ends"`
	Issuer      string     `json:"issuer,omitempty" doc:"Who issued the certificate served"`
	Error       string     `json:"error,omitempty" doc:"Why the connection failed, as Go put it"`
	CheckedAt   time.Time  `json:"checkedAt"`
}

// CertificateChecksBody is what the monitor knows of a certificate.
type CertificateChecksBody struct {
	Tracked bool              `json:"tracked" doc:"The monitor knows the version the certificate uses"`
	CheckAt *time.Time        `json:"checkAt,omitempty" doc:"When its domains are checked next"`
	Results []DomainCheckBody `json:"results" doc:"What each domain served the last time it was checked, in the order of the certificate's domains, and for a domain checked apart for mainland China and elsewhere, mainland China first"`
	Skipped []string          `json:"skipped" doc:"Wildcard domains, which cannot be connected to"`
}

// CheckTallyBody sums up the checks of a certificate.
type CheckTallyBody struct {
	CertificateID int64 `json:"certificateId"`
	// Old counts the domains that still serve an older certificate.
	Old int `json:"old" doc:"The domains that still serve an older certificate"`
	// Unreachable counts those it could not connect to.
	Unreachable int `json:"unreachable"`
}

type idInput struct {
	ID int64 `path:"id"`
}

type certificateOutput struct{ Body CertificateChecksBody }

type statesOutput struct {
	Body struct {
		Certificates []CheckTallyBody `json:"certificates"`
	}
}

func registerAPI(a huma.API, m *Module) {
	huma.Register(a, huma.Operation{
		OperationID: "monitor-certificate", Method: http.MethodGet, Path: "/certificates/{id}",
		Summary: "Tell what the domains of a certificate served the last time they were checked",
	}, func(ctx context.Context, in *idInput) (*certificateOutput, error) {
		return m.output(ctx, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "monitor-check", Method: http.MethodPost, Path: "/certificates/{id}/check",
		Summary: "Check the domains of a certificate now",
	}, func(ctx context.Context, in *idInput) (*certificateOutput, error) {
		if _, err := m.check(ctx, in.ID); errors.Is(err, errUnknown) {
			return nil, api.NewError(http.StatusNotFound, i18n.MonitorErrorUnknown, nil)
		} else if err != nil {
			return nil, err
		}
		return m.output(ctx, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "monitor-states", Method: http.MethodGet, Path: "/states",
		Summary: "Sum up the checks of every certificate",
	}, func(ctx context.Context, _ *struct{}) (*statesOutput, error) {
		rows, err := m.db.QueryContext(ctx,
			`SELECT certificate_id, count(CASE WHEN state = 'old' THEN 1 END), count(CASE WHEN state = 'unreachable' THEN 1 END) FROM monitor_results
			 GROUP BY certificate_id ORDER BY certificate_id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := &statesOutput{}
		out.Body.Certificates = []CheckTallyBody{}
		for rows.Next() {
			var s CheckTallyBody
			if err := rows.Scan(&s.CertificateID, &s.Old, &s.Unreachable); err != nil {
				return nil, err
			}
			out.Body.Certificates = append(out.Body.Certificates, s)
		}
		return out, rows.Err()
	})
}

// output tells what the monitor knows of certificate id.
func (m *Module) output(ctx context.Context, id int64) (*certificateOutput, error) {
	out := &certificateOutput{Body: CertificateChecksBody{Results: []DomainCheckBody{}, Skipped: []string{}}}
	t, err := m.load(ctx, m.db, id)
	if errors.Is(err, errUnknown) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.Body.Tracked = true
	checkAt := t.checkAt.UTC()
	out.Body.CheckAt = &checkAt
	rows, err := m.db.QueryContext(ctx,
		"SELECT domain, view, state, fingerprint, not_after, issuer, error, checked_at FROM monitor_results WHERE certificate_id = ? ORDER BY view", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDomain := map[string][]DomainCheckBody{}
	for rows.Next() {
		var r DomainCheckBody
		var notAfter, checked int64
		if err := rows.Scan(&r.Domain, &r.View, &r.State, &r.Fingerprint, &notAfter, &r.Issuer, &r.Error, &checked); err != nil {
			return nil, err
		}
		if notAfter != 0 {
			na := time.UnixMilli(notAfter).UTC()
			r.NotAfter = &na
		}
		r.CheckedAt = time.UnixMilli(checked).UTC()
		byDomain[r.Domain] = append(byDomain[r.Domain], r) // '', then cn, then global
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, d := range t.domains {
		switch r, ok := byDomain[d]; {
		case strings.HasPrefix(d, "*."):
			out.Body.Skipped = append(out.Body.Skipped, d)
		case ok:
			out.Body.Results = append(out.Body.Results, r...)
		}
	}
	slices.Sort(out.Body.Skipped)
	return out, nil
}

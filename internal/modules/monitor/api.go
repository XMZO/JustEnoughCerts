package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

// DomainCheckBody is what a domain served the last time it was checked.
type DomainCheckBody struct {
	Domain      string     `json:"domain" doc:"The domain, with its port when not 443, as mail.example.com:993"`
	View        string     `json:"view,omitempty" enum:"cn,global" doc:"cn: as visitors from mainland China reach the domain; global: as those from elsewhere do; absent when its DNS sends them the same way"`
	State       string     `json:"state" enum:"ok,old,other,unreachable" doc:"ok: the version in use; old: an older certificate for the domain from the same CA; other: someone else's certificate, such as a CDN's own, or one for other names, or a newer one; unreachable: nothing over TLS on the port"`
	Fingerprint string     `json:"fingerprint,omitempty" doc:"SHA-256 of the certificate served, lowercase hex"`
	NotAfter    *time.Time `json:"notAfter,omitempty" doc:"When the certificate served ends"`
	Issuer      string     `json:"issuer,omitempty" doc:"Who issued the certificate served"`
	Error       string     `json:"error,omitempty" doc:"Why the connection failed, as Go put it"`
	Incomplete  bool       `json:"incomplete,omitempty" doc:"The version in use was served without the intermediates clients need: browsers on computers fetch them, many apps, Android and curl fail"`
	CheckedAt   time.Time  `json:"checkedAt"`
}

// CertificateChecksBody is what the monitor knows of a certificate.
type CertificateChecksBody struct {
	Tracked bool              `json:"tracked" doc:"The monitor knows the version the certificate uses"`
	Ports   []int             `json:"ports" doc:"The ports its domains are checked on; on 25 and 587, through SMTP's STARTTLS"`
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
	// Incomplete counts those that serve the version without its
	// intermediates.
	Incomplete int `json:"incomplete" doc:"The domains that serve the version in use without the intermediates clients need"`
}

type idInput struct {
	ID int64 `path:"id"`
}

type portsInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Ports []int `json:"ports" minItems:"1" maxItems:"10" doc:"Ports from 1 to 65535, as 443 and 993"`
	}
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
		OperationID: "monitor-ports", Method: http.MethodPut, Path: "/certificates/{id}/ports",
		Summary:     "Set the ports the domains of a certificate are checked on, and check them now",
		Description: "As 443 for a website and 993 for IMAP; on 25 and 587, TLS comes through SMTP's STARTTLS.",
	}, func(ctx context.Context, in *portsInput) (*certificateOutput, error) {
		var ports []string
		for i, p := range in.Body.Ports {
			if p < 1 || p > 65535 {
				return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil).
					WithField(fmt.Sprintf("body.ports[%d]", i), i18n.KernelErrorInvalidField)
			}
			if s := strconv.Itoa(p); !slices.Contains(ports, s) {
				ports = append(ports, s)
			}
		}
		res, err := m.db.ExecContext(ctx, "UPDATE monitor_certificates SET ports = ? WHERE certificate_id = ?", strings.Join(ports, ","), in.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, api.NewError(http.StatusNotFound, i18n.MonitorErrorUnknown, nil)
		}
		if _, err := m.check(ctx, in.ID); err != nil {
			return nil, err
		}
		return m.output(ctx, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "monitor-states", Method: http.MethodGet, Path: "/states",
		Summary: "Sum up the checks of every certificate",
	}, func(ctx context.Context, _ *struct{}) (*statesOutput, error) {
		rows, err := m.db.QueryContext(ctx,
			`SELECT certificate_id, count(CASE WHEN state = 'old' THEN 1 END), count(CASE WHEN state = 'unreachable' THEN 1 END),
			   count(CASE WHEN state = 'ok' AND incomplete = 1 THEN 1 END)
			 FROM monitor_results GROUP BY certificate_id ORDER BY certificate_id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := &statesOutput{}
		out.Body.Certificates = []CheckTallyBody{}
		for rows.Next() {
			var s CheckTallyBody
			if err := rows.Scan(&s.CertificateID, &s.Old, &s.Unreachable, &s.Incomplete); err != nil {
				return nil, err
			}
			out.Body.Certificates = append(out.Body.Certificates, s)
		}
		return out, rows.Err()
	})
}

// output tells what the monitor knows of certificate id.
func (m *Module) output(ctx context.Context, id int64) (*certificateOutput, error) {
	out := &certificateOutput{Body: CertificateChecksBody{Results: []DomainCheckBody{}, Skipped: []string{}, Ports: []int{defaultPort}}}
	t, err := m.load(ctx, m.db, id)
	if errors.Is(err, errUnknown) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.Body.Tracked = true
	out.Body.Ports = t.ports
	checkAt := t.checkAt.UTC()
	out.Body.CheckAt = &checkAt
	rows, err := m.db.QueryContext(ctx,
		"SELECT domain, view, state, fingerprint, not_after, issuer, error, checked_at, incomplete FROM monitor_results WHERE certificate_id = ? ORDER BY view", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDomain := map[string][]DomainCheckBody{}
	for rows.Next() {
		var r DomainCheckBody
		var notAfter, checked, incomplete int64
		if err := rows.Scan(&r.Domain, &r.View, &r.State, &r.Fingerprint, &notAfter, &r.Issuer, &r.Error, &checked, &incomplete); err != nil {
			return nil, err
		}
		r.Incomplete = incomplete == 1 && r.State == stateOK
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
		if strings.HasPrefix(d, "*.") {
			out.Body.Skipped = append(out.Body.Skipped, d)
			continue
		}
		for _, p := range t.ports {
			out.Body.Results = append(out.Body.Results, byDomain[target(d, p)]...)
		}
	}
	slices.Sort(out.Body.Skipped)
	return out, nil
}

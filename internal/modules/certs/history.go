package certs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

// maxRuns bounds the runs one answer of the history holds.
const maxRuns = 500

// Kinds of run.
const (
	runIssuance   = "issuance"
	runDeployment = "deployment"
)

// Run is an issuance or a deployment, as the history lists them.
type Run struct {
	Kind          string
	ID            int64
	CertificateID int64
	Domain        string // the certificate's primary domain
	Reason        string // the issuance's or the deployment's
	State         string
	Attempts      int
	Target        int64 // of a deployment
	Error         contracts.Message
	// NotAfter is the end of the version issued or deployed; zero when
	// there is none yet.
	NotAfter   time.Time
	CreatedAt  time.Time
	StartedAt  time.Time // of the last attempt; zero before the first
	FinishedAt time.Time
}

// runs returns the issuances and deployments created since since, the
// newest first, at most limit of them, and whether there were more.
func (s *service) runs(ctx context.Context, since time.Time, limit int) ([]Run, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT 'issuance', i.id, i.certificate_id, json_extract(c.domains, '$[0]'), i.reason, i.state, i.attempts, 0,
		       i.error_key, i.error_params, v.not_after, i.created_at, i.started_at, i.finished_at
		FROM certs_issuances i
		JOIN certs_certificates c ON c.id = i.certificate_id
		LEFT JOIN certs_versions v ON v.id = i.version_id
		WHERE i.created_at >= ?
		UNION ALL
		SELECT 'deployment', d.id, d.certificate_id, json_extract(c.domains, '$[0]'), d.reason, d.state, d.attempts, d.provider_id,
		       d.error_key, d.error_params, v.not_after, d.created_at, d.started_at, d.finished_at
		FROM certs_deployments d
		JOIN certs_certificates c ON c.id = d.certificate_id
		LEFT JOIN certs_versions v ON v.id = d.version_id
		WHERE d.created_at >= ?
		ORDER BY 12 DESC, 1, 2 DESC
		LIMIT ?`, since.UnixMilli(), since.UnixMilli(), limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var domain, params sql.NullString
		var notAfter, started, finished sql.NullInt64
		var created int64
		// The key is scanned as it is: it was stored from a message.
		if err := rows.Scan(&r.Kind, &r.ID, &r.CertificateID, &domain, &r.Reason, &r.State, &r.Attempts, &r.Target,
			&r.Error.Key, &params, &notAfter, &created, &started, &finished); err != nil {
			return nil, false, err
		}
		r.Domain = domain.String
		if params.String != "" {
			if err := json.Unmarshal([]byte(params.String), &r.Error.Params); err != nil {
				return nil, false, fmt.Errorf("%s %d: error params: %w", r.Kind, r.ID, err)
			}
		}
		r.CreatedAt = time.UnixMilli(created).UTC()
		if notAfter.Valid {
			r.NotAfter = time.UnixMilli(notAfter.Int64).UTC()
		}
		if started.Valid {
			r.StartedAt = time.UnixMilli(started.Int64).UTC()
		}
		if finished.Valid {
			r.FinishedAt = time.UnixMilli(finished.Int64).UTC()
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// RunBody is a run as the API returns it.
type RunBody struct {
	Kind        string       `json:"kind" enum:"issuance,deployment"`
	ID          int64        `json:"id"`
	Certificate int64        `json:"certificate"`
	Domain      string       `json:"domain" doc:"The certificate's primary domain"`
	Action      string       `json:"action" enum:"issue,renew,deploy" doc:"renew: an issuance because the current version entered its renewal window"`
	Trigger     string       `json:"trigger" enum:"auto,manual" doc:"manual: someone added the certificate or a target, or asked for it"`
	State       string       `json:"state" enum:"pending,running,waiting,done,failed,canceled" doc:"waiting: an issuance whose source is not done yet"`
	Attempts    int          `json:"attempts"`
	Target      int64        `json:"target,omitempty" doc:"The target of a deployment"`
	Error       *MessageBody `json:"error,omitempty" doc:"Why the last attempt failed"`
	NotAfter    *time.Time   `json:"notAfter,omitempty" doc:"The end of the version issued or deployed"`
	CreatedAt   time.Time    `json:"createdAt"`
	StartedAt   *time.Time   `json:"startedAt,omitempty" doc:"Of the last attempt"`
	FinishedAt  *time.Time   `json:"finishedAt,omitempty"`
}

func runBody(r Run) RunBody {
	b := RunBody{
		Kind: r.Kind, ID: r.ID, Certificate: r.CertificateID, Domain: r.Domain, State: r.State, Attempts: r.Attempts,
		Target: r.Target, CreatedAt: r.CreatedAt, Action: "deploy", Trigger: "manual",
	}
	switch {
	case r.Kind == runIssuance && r.Reason == reasonRenewal:
		b.Action, b.Trigger = "renew", "auto"
	case r.Kind == runIssuance:
		b.Action = "issue"
	case r.Reason == deployVersion:
		b.Trigger = "auto"
	}
	if r.Error.Key != "" {
		b.Error = &MessageBody{Key: r.Error.Key, Params: r.Error.Params}
	}
	if !r.NotAfter.IsZero() {
		b.NotAfter = timePtr(r.NotAfter)
	}
	if !r.StartedAt.IsZero() {
		b.StartedAt = timePtr(r.StartedAt)
	}
	if !r.FinishedAt.IsZero() {
		b.FinishedAt = timePtr(r.FinishedAt)
	}
	return b
}

type runsInput struct {
	Days int `query:"days" minimum:"0" maximum:"36500" default:"30" doc:"How many days back to list; 0 for all"`
}

type runsOutput struct {
	Body struct {
		Runs []RunBody `json:"runs" doc:"The newest first"`
		More bool      `json:"more" doc:"There are older ones in the time asked for than one answer holds"`
	}
}

func registerHistoryAPI(a huma.API, s *service) {
	huma.Register(a, huma.Operation{
		OperationID: "certs-runs", Method: http.MethodGet, Path: "/runs",
		Summary:     "List the issuances and deployments",
		Description: fmt.Sprintf("Those created in the last days, the newest first, at most %d.", maxRuns),
	}, func(ctx context.Context, in *runsInput) (*runsOutput, error) {
		var since time.Time
		if in.Days > 0 {
			since = s.now().Add(-time.Duration(in.Days) * day)
		}
		runs, more, err := s.runs(ctx, since, maxRuns)
		if err != nil {
			return nil, err
		}
		out := &runsOutput{}
		out.Body.More = more
		out.Body.Runs = make([]RunBody, 0, len(runs))
		for _, r := range runs {
			out.Body.Runs = append(out.Body.Runs, runBody(r))
		}
		return out, nil
	})
}

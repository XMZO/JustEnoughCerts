package certs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

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

// runQuery is what a page of the history asks for: the runs created since
// since, of an action and a result ("" for any), whose primary domain
// holds text, from offset on, at most limit of them.
type runQuery struct {
	since          time.Time
	action, result string
	text           string
	offset, limit  int
}

// runTally counts the runs of a query, but for its result, by result.
type runTally struct{ all, ok, failed, running, canceled int }

// runsFrom are the runs as the history sees them, with what it filters by:
// when each is shown as happening, the start of its last attempt or else
// its creation, its action, and its result, where a pending run that failed
// before counts as failed, as it waits to try again.
const runsFrom = `
	WITH base AS (
		SELECT 'issuance' AS kind, i.id, i.certificate_id, json_extract(c.domains, '$[0]') AS domain, i.reason, i.state, i.attempts,
		       0 AS target, i.error_key, i.error_params, v.not_after, i.created_at, i.started_at, i.finished_at
		FROM certs_issuances i
		JOIN certs_certificates c ON c.id = i.certificate_id
		LEFT JOIN certs_versions v ON v.id = i.version_id
		WHERE i.created_at >= :since
		UNION ALL
		SELECT 'deployment', d.id, d.certificate_id, json_extract(c.domains, '$[0]'), d.reason, d.state, d.attempts,
		       d.provider_id, d.error_key, d.error_params, v.not_after, d.created_at, d.started_at, d.finished_at
		FROM certs_deployments d
		JOIN certs_certificates c ON c.id = d.certificate_id
		LEFT JOIN certs_versions v ON v.id = d.version_id
		WHERE d.created_at >= :since
	), tagged AS (
		SELECT *, coalesce(started_at, created_at) AS at,
		       CASE WHEN kind = 'deployment' THEN 'deploy' WHEN reason = :renewal THEN 'renew' ELSE 'issue' END AS action,
		       CASE WHEN state = 'done' THEN 'ok'
		            WHEN state = 'failed' OR (state = 'pending' AND error_key <> '') THEN 'failed'
		            WHEN state = 'canceled' THEN 'canceled'
		            ELSE 'running' END AS result
		FROM base
	), runs AS (
		SELECT * FROM tagged WHERE (:action = '' OR action = :action) AND (:text = '' OR domain LIKE :like ESCAPE '\')
	)`

// runs returns the runs q asks for, the newest first, and their tally.
func (s *service) runs(ctx context.Context, q runQuery) ([]Run, runTally, error) {
	var tally runTally
	like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.ToLower(q.text)) + "%"
	args := []any{
		sql.Named("since", q.since.UnixMilli()), sql.Named("renewal", reasonRenewal),
		sql.Named("action", q.action), sql.Named("text", q.text), sql.Named("like", like),
	}
	rows, err := s.db.QueryContext(ctx, runsFrom+` SELECT result, count(*) FROM runs GROUP BY result`, args...)
	if err != nil {
		return nil, tally, err
	}
	defer rows.Close()
	for rows.Next() {
		var result string
		var n int
		if err := rows.Scan(&result, &n); err != nil {
			return nil, tally, err
		}
		tally.all += n
		switch result {
		case "ok":
			tally.ok = n
		case "failed":
			tally.failed = n
		case "running":
			tally.running = n
		case "canceled":
			tally.canceled = n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, tally, err
	}

	rows, err = s.db.QueryContext(ctx, runsFrom+`
		SELECT kind, id, certificate_id, domain, reason, state, attempts, target, error_key, error_params, not_after, created_at, started_at, finished_at
		FROM runs WHERE :result = '' OR result = :result
		ORDER BY at DESC, kind, id DESC LIMIT :limit OFFSET :offset`,
		append(args, sql.Named("result", q.result), sql.Named("limit", q.limit), sql.Named("offset", q.offset))...)
	if err != nil {
		return nil, tally, err
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
			return nil, tally, err
		}
		r.Domain = domain.String
		if params.String != "" {
			if err := json.Unmarshal([]byte(params.String), &r.Error.Params); err != nil {
				return nil, tally, fmt.Errorf("%s %d: error params: %w", r.Kind, r.ID, err)
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
	return out, tally, rows.Err()
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
	Days   int    `query:"days" minimum:"0" maximum:"36500" default:"30" doc:"How many days back to list; 0 for all"`
	Action string `query:"action" enum:"issue,renew,deploy" doc:"Only runs of this action"`
	Result string `query:"result" enum:"ok,failed,running,canceled" doc:"Only runs of this result; a pending run that failed before is failed"`
	Q      string `query:"q" maxLength:"200" doc:"Only runs of certificates whose primary domain holds this text"`
	Page   int    `query:"page" minimum:"1" default:"1"`
	Size   int    `query:"size" minimum:"1" maximum:"5000" default:"500" doc:"Runs a page holds"`
}

// RunCounts counts the runs the time, action and text ask for, by result.
type RunCounts struct {
	All      int `json:"all"`
	OK       int `json:"ok"`
	Failed   int `json:"failed"`
	Running  int `json:"running"`
	Canceled int `json:"canceled"`
}

type runsOutput struct {
	Body struct {
		Runs   []RunBody `json:"runs" doc:"The newest first, by the start of their last attempt or else their creation"`
		Total  int       `json:"total" doc:"The runs all filters ask for, every page"`
		Counts RunCounts `json:"counts"`
		More   bool      `json:"more" doc:"There are pages after this one"`
	}
}

func registerHistoryAPI(a huma.API, s *service) {
	huma.Register(a, huma.Operation{
		OperationID: "certs-runs", Method: http.MethodGet, Path: "/runs",
		Summary:     "List the issuances and deployments",
		Description: "Those created in the last days that the filters ask for, the newest first, a page at a time.",
	}, func(ctx context.Context, in *runsInput) (*runsOutput, error) {
		q := runQuery{action: in.Action, result: in.Result, text: strings.TrimSpace(in.Q), offset: (in.Page - 1) * in.Size, limit: in.Size}
		if in.Days > 0 {
			q.since = s.now().Add(-time.Duration(in.Days) * day)
		}
		runs, tally, err := s.runs(ctx, q)
		if err != nil {
			return nil, err
		}
		out := &runsOutput{}
		out.Body.Counts = RunCounts{All: tally.all, OK: tally.ok, Failed: tally.failed, Running: tally.running, Canceled: tally.canceled}
		out.Body.Total = tally.all
		if q.result != "" {
			out.Body.Total = map[string]int{"ok": tally.ok, "failed": tally.failed, "running": tally.running, "canceled": tally.canceled}[q.result]
		}
		out.Body.More = q.offset+len(runs) < out.Body.Total
		out.Body.Runs = make([]RunBody, 0, len(runs))
		for _, r := range runs {
			out.Body.Runs = append(out.Body.Runs, runBody(r))
		}
		return out, nil
	})
}

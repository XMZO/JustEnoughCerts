package certs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// defaultKeyType is the key type of new certificates until the settings
// say otherwise: RSA still works with the most clients.
const defaultKeyType = "rsa-2048"

// IssuanceBody is an issuance as the API returns it.
type IssuanceBody struct {
	ID            int64        `json:"id"`
	CertificateID int64        `json:"certificateId"`
	Reason        string       `json:"reason" enum:"first,renewal,manual" doc:"first: the certificate was added, or has no version yet; renewal; manual: someone asked"`
	State         string       `json:"state" enum:"pending,running,waiting,done,failed" doc:"pending: the next attempt starts at nextAt, a retry when error is set; waiting: the source is not done, as when a CA reviews the order, and is asked again at nextAt; failed: no attempt follows unless someone starts one"`
	Attempts      int          `json:"attempts" doc:"Attempts started so far; waiting for the source goes on with an attempt"`
	NextAt        *time.Time   `json:"nextAt,omitempty" doc:"When the next attempt starts, for a pending issuance, or when the source is asked again, for a waiting one"`
	VersionID     int64        `json:"versionId,omitempty" doc:"The version issued"`
	Error         *MessageBody `json:"error,omitempty" doc:"Why the last attempt failed"`
	CreatedAt     time.Time    `json:"createdAt"`
	StartedAt     *time.Time   `json:"startedAt,omitempty" doc:"When the last attempt started"`
	FinishedAt    *time.Time   `json:"finishedAt,omitempty"`
}

// MessageBody is a message key with its arguments, which the client shows
// in the viewer's language.
type MessageBody struct {
	Key    i18n.Key       `json:"key"`
	Params map[string]any `json:"params,omitempty"`
}

// LogLineBody is a line of an issuance's log.
type LogLineBody struct {
	Seq     int            `json:"seq"`
	At      time.Time      `json:"at"`
	Attempt int            `json:"attempt"`
	Level   string         `json:"level" enum:"info,ok,warn,error"`
	Step    string         `json:"step,omitempty" enum:"key,order,challenge,validate,cleanup,download" doc:"The step the line belongs to, if any"`
	Key     i18n.Key       `json:"key"`
	Params  map[string]any `json:"params,omitempty"`
	Detail  string         `json:"detail,omitempty" doc:"Raw text behind the line, such as a CA's error, in its own language"`
}

// StepBody tells how far a step of the last attempt got.
type StepBody struct {
	Step  string     `json:"step" enum:"key,order,challenge,validate,cleanup,download"`
	State string     `json:"state" enum:"pending,running,done,failed"`
	At    *time.Time `json:"at,omitempty" doc:"The time of the step's last line"`
}

func logLineBody(l LogEntry) LogLineBody {
	return LogLineBody{
		Seq: l.Seq, At: l.At, Attempt: l.Attempt, Level: string(l.Level), Step: string(l.Step),
		Key: l.Key, Params: l.Params, Detail: l.Detail,
	}
}

func issuanceBody(is *Issuance) IssuanceBody {
	b := IssuanceBody{
		ID: is.ID, CertificateID: is.CertificateID, Reason: is.Reason, State: is.State, Attempts: is.Attempts,
		VersionID: is.VersionID, CreatedAt: is.CreatedAt,
	}
	if is.State == issuancePending || is.State == issuanceWaiting {
		b.NextAt = timePtr(is.NextAt)
	}
	if !is.StartedAt.IsZero() {
		b.StartedAt = timePtr(is.StartedAt)
	}
	if !is.FinishedAt.IsZero() {
		b.FinishedAt = timePtr(is.FinishedAt)
	}
	if is.Error.Key != "" {
		b.Error = &MessageBody{Key: is.Error.Key, Params: is.Error.Params}
	}
	return b
}

func timePtr(t time.Time) *time.Time { return &t }

// IssueRequest asks for a certificate from a source provider.
type IssueRequest struct {
	Domains         []string `json:"domains" minItems:"1" maxItems:"100" doc:"The domains, the primary first; a wildcard as *.example.com, an internationalized name in any form"`
	SourceProvider  int64    `json:"sourceProvider" doc:"The source provider instance that issues the certificate"`
	SolverProvider  int64    `json:"solverProvider,omitempty" doc:"The solver provider instance that proves control of the domains, for a source that needs one"`
	KeyType         string   `json:"keyType,omitempty" enum:"ecdsa-p256,ecdsa-p384,rsa-2048,rsa-3072,rsa-4096" doc:"The type of the private key, new for each version; that of the settings if left out"`
	RenewBeforeDays int      `json:"renewBeforeDays,omitempty" minimum:"1" maximum:"365" doc:"Days before the end to renew; that of the settings if left out"`
	Targets         []int64  `json:"targets,omitempty" maxItems:"50" doc:"The target provider instances to deploy each version to, in order"`
}

type issueInput struct {
	Body IssueRequest
}

type issuanceOutput struct {
	Body IssuanceBody
}

type issuanceListInput struct {
	ID    int64 `path:"id"`
	Limit int   `query:"limit" minimum:"1" maximum:"100" default:"20" doc:"How many, the newest first"`
}

type issuanceListOutput struct {
	Body struct {
		Issuances []IssuanceBody `json:"issuances" doc:"The newest first"`
	}
}

type issuanceDetailOutput struct {
	Body struct {
		Issuance    IssuanceBody    `json:"issuance"`
		Certificate CertificateBody `json:"certificate"`
		Steps       []StepBody      `json:"steps" doc:"The steps of the last attempt, in order"`
		Log         []LogLineBody   `json:"log" doc:"Every line, of every attempt, in order"`
	}
}

func registerIssueAPI(a huma.API, s *service) {
	huma.Register(a, huma.Operation{
		OperationID: "certs-issue-new", Method: http.MethodPost, Path: "/certificates/issue", DefaultStatus: http.StatusCreated,
		Summary:     "Add a certificate issued by a source provider",
		Description: "The certificate is added at once; its first issuance runs in the background.",
	}, func(ctx context.Context, in *issueInput) (*certificateOutput, error) {
		id, err := s.addIssued(ctx, &in.Body)
		if err != nil {
			return nil, err
		}
		return s.output(ctx, id)
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-issue", Method: http.MethodPost, Path: "/certificates/{id}/issue", DefaultStatus: http.StatusAccepted,
		Summary:     "Issue a new version of a certificate now",
		Description: "Starts an issuance, or returns the one under way, which is retried now if it was waiting to.",
	}, func(ctx context.Context, in *idInput) (*issuanceOutput, error) {
		id, err := s.issue(ctx, in.ID)
		if err != nil {
			return nil, serviceError(err)
		}
		is, err := s.issuance(ctx, id)
		if err != nil {
			return nil, err
		}
		return &issuanceOutput{Body: issuanceBody(is)}, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-set-domains", Method: http.MethodPut, Path: "/certificates/{id}/domains",
		Summary:     "Change the domains of a certificate with a source, which is issued again at once",
		Description: "Refused while an issuance is under way; an uploaded certificate takes its domains from its file.",
	}, func(ctx context.Context, in *domainsInput) (*certificateOutput, error) {
		if err := s.checkDomains(ctx, in.ID, in.Body.Domains); err != nil {
			return nil, err
		}
		domains, _ := normalizeDomains(in.Body.Domains)
		if err := s.setDomains(ctx, in.ID, domains); err != nil {
			return nil, serviceError(err)
		}
		return s.output(ctx, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-issuances", Method: http.MethodGet, Path: "/certificates/{id}/issuances",
		Summary: "List the latest issuances of a certificate",
	}, func(ctx context.Context, in *issuanceListInput) (*issuanceListOutput, error) {
		if _, err := s.load(ctx, s.db, in.ID); err != nil {
			return nil, serviceError(err)
		}
		list, err := s.issuances(ctx, in.ID, in.Limit)
		if err != nil {
			return nil, err
		}
		out := &issuanceListOutput{}
		out.Body.Issuances = make([]IssuanceBody, 0, len(list))
		for _, is := range list {
			out.Body.Issuances = append(out.Body.Issuances, issuanceBody(is))
		}
		return out, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-issuance", Method: http.MethodGet, Path: "/issuances/{id}",
		Summary: "Get an issuance with its steps and log",
	}, func(ctx context.Context, in *idInput) (*issuanceDetailOutput, error) {
		is, err := s.issuance(ctx, in.ID)
		if err != nil {
			return nil, serviceError(err)
		}
		c, err := s.load(ctx, s.db, is.CertificateID)
		if err != nil {
			return nil, serviceError(err)
		}
		lines, err := s.logLines(ctx, issuanceLogTable, is.ID)
		if err != nil {
			return nil, err
		}
		out := &issuanceDetailOutput{}
		out.Body.Issuance = issuanceBody(is)
		if out.Body.Certificate, err = s.body(ctx, c); err != nil {
			return nil, err
		}
		out.Body.Steps = []StepBody{}
		for _, st := range steps(is, lines, c.SolverProvider != 0) {
			b := StepBody{Step: string(st.Step), State: st.State}
			if !st.At.IsZero() {
				b.At = timePtr(st.At)
			}
			out.Body.Steps = append(out.Body.Steps, b)
		}
		out.Body.Log = make([]LogLineBody, 0, len(lines))
		for _, l := range lines {
			out.Body.Log = append(out.Body.Log, logLineBody(l))
		}
		return out, nil
	})
}

type domainsInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Domains []string `json:"domains" minItems:"1" maxItems:"100" doc:"The domains, the primary first; a wildcard as *.example.com"`
	}
}

// checkDomains checks new domains of certificate id as adding it does:
// each well formed, and each one its solver can prove.
func (s *service) checkDomains(ctx context.Context, id int64, names []string) error {
	c, err := s.load(ctx, s.db, id)
	if err != nil {
		return serviceError(err)
	}
	invalid := api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
	domains, bad := normalizeDomains(names)
	for _, i := range bad {
		invalid = invalid.WithField(fmt.Sprintf("body.domains[%d]", i), i18n.CertsErrorBadDomain)
	}
	if c.SolverProvider != 0 {
		if typ, err := s.providers.Type(ctx, c.SolverProvider); err == nil {
			switch {
			case typ.Solves() == providers.ChallengeHTTP && slices.ContainsFunc(domains, isWildcard):
				invalid = invalid.WithField("body.domains", i18n.CertsErrorWildcardHttp)
			case typ.Solves() != providers.ChallengeHTTP && slices.ContainsFunc(domains, isIP):
				invalid = invalid.WithField("body.domains", i18n.CertsErrorIpDns)
			}
		}
	}
	if len(invalid.Fields) > 0 {
		return invalid
	}
	return nil
}

// addIssued checks the request and adds the certificate with its first
// issuance, which it hands to the runner.
func (s *service) addIssued(ctx context.Context, in *IssueRequest) (int64, error) {
	invalid := api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
	domains, bad := normalizeDomains(in.Domains)
	for _, i := range bad {
		invalid = invalid.WithField(fmt.Sprintf("body.domains[%d]", i), i18n.CertsErrorBadDomain)
	}
	var source *providers.Type
	if code := s.checkProvider(ctx, in.SourceProvider, providers.Source); code != "" {
		invalid = invalid.WithField("body.sourceProvider", code)
	} else if typ, err := s.providers.Type(ctx, in.SourceProvider); err == nil {
		source = &typ
		if typ.NeedsSolver && in.SolverProvider == 0 {
			invalid = invalid.WithField("body.solverProvider", i18n.KernelErrorFieldRequired)
		}
	}
	if in.SolverProvider != 0 {
		if code := s.checkProvider(ctx, in.SolverProvider, providers.Solver); code != "" {
			invalid = invalid.WithField("body.solverProvider", code)
		} else if typ, err := s.providers.Type(ctx, in.SolverProvider); err == nil {
			switch {
			case source != nil && source.NeedsSolver && !source.Takes(typ):
				invalid = invalid.WithField("body.solverProvider", i18n.CertsErrorSolverChallenge)
			case typ.Solves() == providers.ChallengeHTTP && slices.ContainsFunc(domains, isWildcard):
				invalid = invalid.WithField("body.solverProvider", i18n.CertsErrorWildcardHttp)
			case typ.Solves() != providers.ChallengeHTTP && slices.ContainsFunc(domains, isIP):
				invalid = invalid.WithField("body.solverProvider", i18n.CertsErrorIpDns)
			}
		}
	}
	targets, badTargets := s.checkTargets(ctx, in.Targets)
	for _, f := range badTargets {
		invalid = invalid.WithField(f.Location, f.Code)
	}
	if len(invalid.Fields) > 0 {
		return 0, invalid
	}
	st, err := s.settings(ctx)
	if err != nil {
		return 0, err
	}
	keyType := in.KeyType
	switch {
	case source != nil && source.OwnKey:
		keyType = "" // the source's own, known once the first version comes
	case keyType == "":
		keyType = st.KeyType
	}
	if keyType != "" && !slices.Contains(cryptox.KeyTypes, keyType) {
		return 0, invalid.WithField("body.keyType", i18n.KernelErrorInvalidField)
	}
	renewBefore := in.RenewBeforeDays
	if renewBefore == 0 {
		renewBefore = st.RenewBeforeDays
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	id, err := s.insertIssued(ctx, tx, Certificate{
		Domains: domains, SourceProvider: in.SourceProvider, SolverProvider: in.SolverProvider,
		KeyType: keyType, RenewBeforeDays: renewBefore, Targets: targets,
	})
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.issuing.kick()
	return id, nil
}

// insertIssued adds c, checked already, as a certificate from its source
// provider, with its first issuance, in tx; the runner is to be woken
// once tx is committed.
func (s *service) insertIssued(ctx context.Context, tx *sql.Tx, c Certificate) (int64, error) {
	now := s.now().UnixMilli()
	domainsJSON, _ := json.Marshal(c.Domains)
	solver := sql.NullInt64{Int64: c.SolverProvider, Valid: c.SolverProvider != 0}
	var id int64
	err := tx.QueryRowContext(ctx,
		`INSERT INTO certs_certificates (domains, source, source_provider, solver_provider, key_type, renew_before_days, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		string(domainsJSON), sourceProvider, c.SourceProvider, solver, c.KeyType, c.RenewBeforeDays, c.Note, now, now).Scan(&id)
	if err != nil {
		return 0, err
	}
	if err := s.setTargets(ctx, tx, &Certificate{ID: id}, c.Targets); err != nil {
		return 0, err
	}
	if _, err := s.startIssuance(ctx, tx, id, reasonFirst); err != nil {
		return 0, err
	}
	return id, nil
}

// checkProvider returns what is wrong with using instance id as a provider
// of kind, or "" if nothing is.
func (s *service) checkProvider(ctx context.Context, id int64, kind providers.Kind) i18n.Key {
	typ, err := s.providers.Type(ctx, id)
	switch {
	case errors.Is(err, providers.ErrNotFound):
		return i18n.KernelErrorNotFound
	case errors.Is(err, providers.ErrUnavailable):
		return i18n.KernelErrorProviderTypeUnknown
	case err != nil:
		s.log.Warn("look up provider", "id", id, "err", err)
		return i18n.KernelErrorInternal
	case typ.Kind != kind:
		return i18n.CertsErrorWrongProvider
	}
	return ""
}

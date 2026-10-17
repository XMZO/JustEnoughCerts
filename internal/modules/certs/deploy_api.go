package certs

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// DeploymentBody is a deployment as the API returns it.
type DeploymentBody struct {
	ID         int64        `json:"id"`
	Target     int64        `json:"target" doc:"The target provider instance"`
	VersionID  int64        `json:"versionId" doc:"The version deployed"`
	Reason     string       `json:"reason" enum:"version,target,manual" doc:"Why it started: a new version came, the target was added, or someone asked"`
	State      string       `json:"state" enum:"pending,running,done,failed,canceled" doc:"pending: the next attempt starts at nextAt, a retry when error is set; failed: no attempt follows unless someone starts one; canceled: the target was taken off, or a newer version came"`
	Attempts   int          `json:"attempts"`
	NextAt     *time.Time   `json:"nextAt,omitempty"`
	Ref        string       `json:"ref,omitempty" doc:"What the target holds, such as a certificate ID at a CDN"`
	Removed    bool         `json:"removed,omitempty" doc:"A later deployment removed it from the target"`
	Error      *MessageBody `json:"error,omitempty" doc:"Why the last attempt failed"`
	CreatedAt  time.Time    `json:"createdAt"`
	StartedAt  *time.Time   `json:"startedAt,omitempty"`
	FinishedAt *time.Time   `json:"finishedAt,omitempty"`
}

func deploymentBody(d *Deployment) DeploymentBody {
	b := DeploymentBody{
		ID: d.ID, Target: d.Target, VersionID: d.VersionID, Reason: d.Reason, State: d.State, Attempts: d.Attempts,
		Ref: d.Ref, Removed: d.Removed, CreatedAt: d.CreatedAt,
	}
	if d.State == issuancePending {
		b.NextAt = timePtr(d.NextAt)
	}
	if !d.StartedAt.IsZero() {
		b.StartedAt = timePtr(d.StartedAt)
	}
	if !d.FinishedAt.IsZero() {
		b.FinishedAt = timePtr(d.FinishedAt)
	}
	if d.Error.Key != "" {
		b.Error = &MessageBody{Key: d.Error.Key, Params: d.Error.Params}
	}
	return b
}

type deployInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Target int64 `json:"target,omitempty" doc:"The target to deploy to; every target of the certificate if left out"`
	}
}

type deploymentDetailOutput struct {
	Body struct {
		Deployment  DeploymentBody  `json:"deployment"`
		Certificate CertificateBody `json:"certificate"`
		Log         []LogLineBody   `json:"log" doc:"Every line, of every attempt, in order"`
	}
}

func registerDeployAPI(a huma.API, s *service) {
	huma.Register(a, huma.Operation{
		OperationID: "certs-deploy", Method: http.MethodPost, Path: "/certificates/{id}/deploy", DefaultStatus: http.StatusAccepted,
		Summary:     "Deploy the current version of a certificate now",
		Description: "Queues the current version for a target, or for every target of the certificate; a deployment waiting to retry starts over.",
	}, func(ctx context.Context, in *deployInput) (*certificateOutput, error) {
		if err := s.deployNow(ctx, in.ID, in.Body.Target); err != nil {
			return nil, serviceError(err)
		}
		return s.output(ctx, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-deployment", Method: http.MethodGet, Path: "/deployments/{id}",
		Summary: "Get a deployment with its log",
	}, func(ctx context.Context, in *idInput) (*deploymentDetailOutput, error) {
		d, err := s.deployment(ctx, in.ID)
		if err != nil {
			return nil, serviceError(err)
		}
		c, err := s.load(ctx, s.db, d.CertificateID)
		if err != nil {
			return nil, serviceError(err)
		}
		lines, err := s.logLines(ctx, deploymentLogTable, d.ID)
		if err != nil {
			return nil, err
		}
		out := &deploymentDetailOutput{}
		out.Body.Deployment = deploymentBody(d)
		if out.Body.Certificate, err = s.body(ctx, c); err != nil {
			return nil, err
		}
		out.Body.Log = make([]LogLineBody, 0, len(lines))
		for _, l := range lines {
			out.Body.Log = append(out.Body.Log, logLineBody(l))
		}
		return out, nil
	})
}

// checkTargets drops repeats from targets and returns what is wrong with
// any of them, located as body.targets[i].
func (s *service) checkTargets(ctx context.Context, targets []int64) ([]int64, []api.FieldError) {
	var out []int64
	var bad []api.FieldError
	for i, t := range targets {
		if code := s.checkProvider(ctx, t, providers.Target); code != "" {
			bad = append(bad, api.FieldError{Location: fmt.Sprintf("body.targets[%d]", i), Code: code})
			continue
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out, bad
}

// validTargets returns targets without repeats, or the error response for
// those that are not targets.
func (s *service) validTargets(ctx context.Context, targets []int64) ([]int64, error) {
	out, bad := s.checkTargets(ctx, targets)
	if len(bad) == 0 {
		return out, nil
	}
	e := api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
	for _, f := range bad {
		e = e.WithField(f.Location, f.Code)
	}
	return nil, e
}

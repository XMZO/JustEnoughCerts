package certs

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func TestRuns(t *testing.T) {
	f := newFixture(t)
	target := f.provider("fake.hook")

	// Forty days ago: an upload, deployed for its new version.
	f.now = start.Add(-40 * day)
	old := f.uploadTo(f.leaf(90, "old.example.com"), target)
	f.runDeploys()

	// Today: a certificate issued, then the upload deployed again by hand,
	// which fails for good.
	f.now = start
	c := f.issueNew("example.com", "www.example.com")
	f.run()
	f.now = start.Add(time.Minute)
	f.fake.deploy = func(context.Context, contracts.DeployRequest) (contracts.DeployResult, error) {
		return contracts.DeployResult{}, retry.Permanent(errors.New("refused"))
	}
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(old.ID, 10)+"/deploy", map[string]any{}), http.StatusAccepted, nil)
	f.runDeploys()

	var out struct {
		Runs []RunBody `json:"runs"`
		More bool      `json:"more"`
	}
	f.call(f.api.Get("/runs?days=30"), http.StatusOK, &out)
	if len(out.Runs) != 2 || out.More {
		t.Fatalf("the last 30 days: %+v", out)
	}
	dep, is := out.Runs[0], out.Runs[1]
	if dep.Kind != "deployment" || dep.Action != "deploy" || dep.Trigger != "manual" || dep.State != "failed" || dep.Target != target ||
		dep.Domain != "old.example.com" || dep.Error == nil || dep.Error.Key != i18n.CertsErrorDeployFailed || dep.NotAfter == nil || dep.FinishedAt == nil {
		t.Errorf("the deployment %+v", dep)
	}
	if is.Kind != "issuance" || is.Action != "issue" || is.Trigger != "manual" || is.State != "done" || is.Certificate != c.ID ||
		is.Domain != "example.com" || is.Error != nil || is.NotAfter == nil || is.Attempts != 1 {
		t.Errorf("the issuance %+v", is)
	}

	f.call(f.api.Get("/runs?days=0"), http.StatusOK, &out)
	if len(out.Runs) != 3 || out.Runs[2].Trigger != "auto" || out.Runs[2].State != "done" {
		t.Errorf("all of them: %+v", out.Runs)
	}
}

func TestRunsLimit(t *testing.T) {
	f := newFixture(t)
	for i := range 3 {
		f.now = start.Add(time.Duration(i) * time.Minute)
		f.issueNew("example" + strconv.Itoa(i) + ".com")
	}
	runs, more, err := f.svc.runs(t.Context(), time.Time{}, 2)
	if err != nil || !more || len(runs) != 2 || runs[0].Domain != "example2.com" || runs[0].State != "pending" {
		t.Errorf("runs %+v, more %v, %v", runs, more, err)
	}
}

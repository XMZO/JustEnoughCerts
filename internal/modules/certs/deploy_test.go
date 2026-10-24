package certs

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// fakeTarget names each deployment ref-<n> and removes what earlier ones
// left, unless the fixture's deploy says otherwise.
type fakeTarget struct{ f *fixture }

func (t *fakeTarget) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	t.f.fake.mu.Lock()
	t.f.fake.deployed = append(t.f.fake.deployed, req)
	n := len(t.f.fake.deployed)
	deploy := t.f.fake.deploy
	t.f.fake.mu.Unlock()
	if deploy != nil {
		return deploy(ctx, req)
	}
	req.Log(contracts.LogLine{Level: contracts.Info, Key: i18n.AcmeLogIssued})
	return contracts.DeployResult{Ref: "ref-" + strconv.Itoa(n), Removed: req.Previous}, nil
}

func (t *fakeTarget) Close() error { return t.f.closed() }

// runDeploys runs the deployment job once, until no deployment is due.
func (f *fixture) runDeploys() {
	f.t.Helper()
	if err := f.svc.runDeployments(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
}

// uploadTo adds an uploaded certificate deployed to targets.
func (f *fixture) uploadTo(u upload, targets ...int64) CertificateBody {
	f.t.Helper()
	var c CertificateBody
	f.call(f.api.Post("/certificates", map[string]any{"certificate": u.cert, "privateKey": u.key, "targets": targets}), http.StatusCreated, &c)
	return c
}

func (f *fixture) uploadVersion(id int64, u upload) {
	f.t.Helper()
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(id, 10)+"/versions", map[string]any{"certificate": u.cert, "privateKey": u.key}), http.StatusCreated, nil)
}

func TestDeployUpload(t *testing.T) {
	f := newFixture(t)
	target := f.provider("fake.hook")
	c := f.uploadTo(f.leaf(90, "example.com"), target)
	if !slices.Equal(c.Targets, []int64{target}) || len(c.Deployments) != 1 || c.State != "deploying" {
		t.Fatalf("added %+v", c)
	}
	if d := c.Deployments[0]; d.State != "pending" || d.Target != target || d.VersionID != c.Current.ID || d.Reason != "version" {
		t.Errorf("deployment %+v", d)
	}

	f.runDeploys()
	c = f.certificate(c.ID)
	d := c.Deployments[0]
	if c.State != "ok" || d.State != "done" || d.Ref != "ref-1" || d.Attempts != 1 || d.FinishedAt == nil {
		t.Fatalf("after the run: %s %+v", c.State, d)
	}
	req := f.fake.deployed[0]
	if req.CertificateID != c.ID || !slices.Equal(req.Domains, []string{"example.com"}) || len(req.Chain) != 2 ||
		req.Key == nil || len(req.Previous) != 0 {
		t.Errorf("request %+v", req)
	}
	if f.fake.opened != 0 {
		t.Errorf("%d providers left open", f.fake.opened)
	}
	if got := eventTypes(f.events()); !slices.Equal(got, []string{contracts.CertificateIssued, contracts.CertificateDeployed}) {
		t.Errorf("events %v", got)
	}
	if ev := f.events()[1].data; ev.Fingerprint != c.Current.Fingerprint || ev.VersionID != c.Current.ID {
		t.Errorf("the deployed event tells %+v", ev)
	}

	var detail struct {
		Deployment DeploymentBody `json:"deployment"`
		Log        []LogLineBody  `json:"log"`
	}
	f.call(f.api.Get("/deployments/"+strconv.FormatInt(d.ID, 10)), http.StatusOK, &detail)
	var keys []i18n.Key
	for _, l := range detail.Log {
		keys = append(keys, l.Key)
	}
	if detail.Deployment.ID != d.ID || !slices.Equal(keys, []i18n.Key{i18n.CertsLogDeploy, i18n.AcmeLogIssued, i18n.CertsLogDeployed}) {
		t.Errorf("detail %+v, log %v", detail.Deployment, keys)
	}
	if detail.Log[0].Params["target"] != "fake.hook" {
		t.Errorf("deploy line %+v", detail.Log[0])
	}
}

func TestDeployReplacesPrevious(t *testing.T) {
	f := newFixture(t)
	target := f.provider("fake.hook")
	c := f.uploadTo(f.leaf(30, "example.com"), target)
	f.runDeploys()
	f.uploadVersion(c.ID, f.leaf(60, "example.com"))
	f.runDeploys()
	f.uploadVersion(c.ID, f.leaf(90, "example.com"))
	f.runDeploys()

	if len(f.fake.deployed) != 3 || len(f.fake.deployed[0].Previous) != 0 ||
		!slices.Equal(f.fake.deployed[1].Previous, []string{"ref-1"}) || !slices.Equal(f.fake.deployed[2].Previous, []string{"ref-2"}) {
		t.Errorf("previous refs: %v, %v, %v", f.fake.deployed[0].Previous, f.fake.deployed[1].Previous, f.fake.deployed[2].Previous)
	}
	if d := f.certificate(c.ID).Deployments[0]; d.Ref != "ref-3" || d.Removed {
		t.Errorf("latest %+v", d)
	}
}

func TestDeployRetries(t *testing.T) {
	f := newFixture(t)
	target := f.provider("fake.hook")
	f.fake.deploy = func(context.Context, contracts.DeployRequest) (contracts.DeployResult, error) {
		return contracts.DeployResult{}, errors.New("connection reset")
	}
	c := f.uploadTo(f.leaf(90, "example.com"), target)
	f.runDeploys()
	c = f.certificate(c.ID)
	d := c.Deployments[0]
	if c.State != "deploy_failed" || d.State != "pending" || d.Attempts != 1 || d.Error == nil || d.Error.Key != i18n.CertsErrorDeployFailed || d.NextAt == nil {
		t.Fatalf("after a failed attempt: %s %+v", c.State, d)
	}
	events := f.events()
	if last := events[len(events)-1]; last.typ != contracts.CertificateDeployFailed || last.data.Deployment.Retry.IsZero() {
		t.Errorf("event %+v", last)
	}

	// A newer version takes the waiting deployment over, from scratch.
	f.fake.deploy = nil
	f.uploadVersion(c.ID, f.leaf(120, "example.com"))
	c = f.certificate(c.ID)
	if d2 := c.Deployments[0]; d2.ID != d.ID || d2.VersionID != c.Current.ID || d2.Attempts != 0 || d2.Error != nil || c.State != "deploying" {
		t.Fatalf("taken over: %s %+v", c.State, d2)
	}
	f.runDeploys()
	if d := f.certificate(c.ID).Deployments[0]; d.State != "done" {
		t.Errorf("after the retry: %+v", d)
	}
}

// TestDeployTimeout: an attempt cut short by its time limit says so,
// whatever the target made of it, and is tried again later.
func TestDeployTimeout(t *testing.T) {
	f := newFixture(t)
	f.svc.deploying.timeout = 50 * time.Millisecond
	target := f.provider("fake.hook")
	f.fake.deploy = func(ctx context.Context, _ contracts.DeployRequest) (contracts.DeployResult, error) {
		<-ctx.Done()
		return contracts.DeployResult{}, &contracts.Problem{Key: i18n.CertsErrorProviderUnavailable, Err: ctx.Err()}
	}
	c := f.uploadTo(f.leaf(90, "example.com"), target)
	f.runDeploys()
	d := f.certificate(c.ID).Deployments[0]
	if d.State != "pending" || d.Error == nil || d.Error.Key != i18n.CertsErrorDeployTimeout || d.NextAt == nil {
		t.Errorf("after the time ran out: %+v", d)
	}
}

func TestDeployPermanentFailure(t *testing.T) {
	f := newFixture(t)
	target := f.provider("fake.hook")
	f.fake.deploy = func(context.Context, contracts.DeployRequest) (contracts.DeployResult, error) {
		return contracts.DeployResult{}, retry.Permanent(&contracts.Problem{Key: i18n.CertsErrorProviderMissing})
	}
	c := f.uploadTo(f.leaf(90, "example.com"), target)
	f.runDeploys()
	if c := f.certificate(c.ID); c.State != "deploy_failed" || c.Deployments[0].State != "failed" || c.Deployments[0].Error.Key != i18n.CertsErrorProviderMissing {
		t.Fatalf("%s, deployment %+v", c.State, c.Deployments[0])
	}

	// Deploying again by hand.
	f.fake.deploy = nil
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/deploy", map[string]any{}), http.StatusAccepted, nil)
	f.runDeploys()
	c = f.certificate(c.ID)
	if d := c.Deployments[0]; d.State != "done" || d.Reason != "manual" || c.State != "ok" {
		t.Errorf("after deploying by hand: %s %+v", c.State, d)
	}
}

func TestTargetsChange(t *testing.T) {
	f := newFixture(t)
	one, two := f.provider("fake.hook"), f.provider("fake.hook")
	f.fake.deploy = func(context.Context, contracts.DeployRequest) (contracts.DeployResult, error) {
		return contracts.DeployResult{}, errors.New("down")
	}
	c := f.uploadTo(f.leaf(90, "example.com"), one)
	f.runDeploys()
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)

	// Adding a target deploys the current version there; taking one off
	// cancels what waits for it.
	f.fake.deploy = nil
	f.call(f.api.Patch(path, map[string]any{"targets": []int64{two}}), http.StatusOK, &c)
	if !slices.Equal(c.Targets, []int64{two}) || len(c.Deployments) != 1 || c.Deployments[0].Target != two || c.Deployments[0].Reason != "target" || c.RenewBeforeDays != 30 {
		t.Fatalf("after the change: %+v", c)
	}
	var state string
	if err := f.db.QueryRowContext(t.Context(), "SELECT state FROM certs_deployments WHERE provider_id = ?", one).Scan(&state); err != nil || state != "canceled" {
		t.Errorf("the old target's deployment: %s, %v", state, err)
	}
	f.runDeploys()
	if len(f.fake.deployed) != 2 || f.fake.deployed[1].CertificateID != c.ID {
		t.Errorf("deployed %d times", len(f.fake.deployed))
	}

	// The renewal window alone leaves the targets as they are.
	f.call(f.api.Patch(path, map[string]any{"renewBeforeDays": 20}), http.StatusOK, &c)
	if c.RenewBeforeDays != 20 || !slices.Equal(c.Targets, []int64{two}) {
		t.Errorf("after changing the window: %+v", c)
	}
	f.call(f.api.Patch(path, map[string]any{"targets": []int64{}}), http.StatusOK, &c)
	if len(c.Targets) != 0 || len(c.Deployments) != 0 {
		t.Errorf("without targets: %+v", c)
	}
}

func TestTargetChecks(t *testing.T) {
	f := newFixture(t)
	source := f.provider("fake.ca")
	u := f.leaf(90, "example.com")
	e := f.apiError(f.api.Post("/certificates", map[string]any{"certificate": u.cert, "privateKey": u.key, "targets": []int64{source, 999}}),
		http.StatusUnprocessableEntity)
	if len(e.Fields) != 2 || e.Fields[0].Location != "body.targets[0]" || e.Fields[0].Code != string(i18n.CertsErrorWrongProvider) ||
		e.Fields[1].Code != string(i18n.KernelErrorNotFound) {
		t.Errorf("errors %+v", e)
	}

	// Deploying needs a version and a target of the certificate.
	c := f.issueNew("issued.example.com")
	path := "/certificates/" + strconv.FormatInt(c.ID, 10) + "/deploy"
	if e := f.apiError(f.api.Post(path, map[string]any{}), http.StatusConflict); e.Code != string(i18n.CertsErrorNoVersion) {
		t.Errorf("without a version: %+v", e)
	}
	up := f.uploadTo(f.leaf(90, "up.example.com"))
	f.apiError(f.api.Post("/certificates/"+strconv.FormatInt(up.ID, 10)+"/deploy", map[string]any{"target": 999}), http.StatusNotFound)
}

func TestDeployAfterIssue(t *testing.T) {
	f := newFixture(t)
	target := f.provider("fake.hook")
	var c CertificateBody
	f.call(f.api.Post("/certificates/issue", map[string]any{
		"domains": []string{"example.com"}, "sourceProvider": f.provider("fake.ca"), "solverProvider": f.provider("fake.dns"),
		"keyType": "ecdsa-p256", "targets": []int64{target},
	}), http.StatusCreated, &c)
	if !slices.Equal(c.Targets, []int64{target}) || len(c.Deployments) != 0 {
		t.Fatalf("added %+v", c)
	}
	f.run()
	if d := f.certificate(c.ID).Deployments; len(d) != 1 || d[0].State != "pending" {
		t.Fatalf("after issuing: %+v", d)
	}
	f.runDeploys()
	if d := f.certificate(c.ID).Deployments[0]; d.State != "done" {
		t.Errorf("after deploying: %+v", d)
	}
}

func TestDeployResumesAndStops(t *testing.T) {
	f := newFixture(t)
	target := f.provider("fake.hook")
	c := f.uploadTo(f.leaf(90, "example.com"), target)
	// An attempt cut short by a stop of JEC starts over.
	if claimed, err := f.svc.claimDeployment(t.Context()); err != nil || claimed == nil {
		t.Fatal(claimed, err)
	}
	f.runDeploys()
	if d := f.certificate(c.ID).Deployments[0]; d.State != "done" || d.Attempts != 2 {
		t.Fatalf("after the restart: %+v", d)
	}

	// Deleting the certificate stops its deployment, with no failure told.
	started := make(chan struct{})
	f.fake.deploy = func(ctx context.Context, _ contracts.DeployRequest) (contracts.DeployResult, error) {
		close(started)
		<-ctx.Done()
		return contracts.DeployResult{}, ctx.Err()
	}
	f.uploadVersion(c.ID, f.leaf(120, "example.com"))
	done := make(chan error)
	go func() { done <- f.svc.runDeployments(t.Context()) }()
	<-started
	f.call(f.api.Delete("/certificates/"+strconv.FormatInt(c.ID, 10)), http.StatusNoContent, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := eventTypes(f.events()); slices.Contains(got, contracts.CertificateDeployFailed) {
		t.Errorf("events %v", got)
	}
}

func TestTargetInUse(t *testing.T) {
	f := newFixture(t)
	target := f.provider("fake.hook")
	c := f.uploadTo(f.leaf(90, "example.com"), target)
	var inUse *providers.InUseError
	if err := f.reg.Delete(t.Context(), target); !errors.As(err, &inUse) {
		t.Errorf("deleting the target: %v", err)
	}
	f.call(f.api.Patch("/certificates/"+strconv.FormatInt(c.ID, 10), map[string]any{"targets": []int64{}}), http.StatusOK, nil)
	if err := f.reg.Delete(t.Context(), target); err != nil {
		t.Errorf("deleting the target once unused: %v", err)
	}
}

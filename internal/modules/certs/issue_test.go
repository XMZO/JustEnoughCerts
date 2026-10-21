package certs

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// fakes are the providers of module "fake": a source that issues from the
// fixture's CA, or does what issue says, and a solver that records calls.
type fakes struct {
	mu    sync.Mutex
	issue func(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error)
	// collect is what the fake source with keys of its own finds.
	collect func(ctx context.Context, req contracts.CollectRequest) (*contracts.Collected, error)
	calls   []string // of the solver
	opened  int      // sources, solvers and targets open
	// deploy replaces the fake target's own work: a ref per deployment,
	// removing the previous ones.
	deploy   func(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error)
	deployed []contracts.DeployRequest
	// renewal is what the fake CA suggests about renewing, nothing when nil;
	// asked counts the questions.
	renewal func(leaf *x509.Certificate) (contracts.RenewalInfo, error)
	asked   int
	// revoke replaces the revoking CA's answer when not nil; revoked lists
	// the serials it revoked, with the reasons.
	revoke  func(leaf *x509.Certificate, reason contracts.RevocationReason) error
	revoked []string
	// spared counts the certificates the CA to fall back on issued.
	spared int
}

func (f *fixture) addFakes() {
	f.t.Helper()
	scope := f.reg.Scope("fake")
	for _, typ := range []providers.Type{
		{ID: "fake.ca", Kind: providers.Source, Brand: "Fake CA", NeedsSolver: true},
		{ID: "fake.free", Kind: providers.Source, Brand: "Fake CA without validation"},
		{ID: "fake.vendor", Kind: providers.Source, Brand: "Fake vendor", NeedsSolver: true, Challenges: []string{providers.ChallengeHTTP, providers.ChallengeRecord}},
		{ID: "fake.dns", Kind: providers.Solver, Brand: "Fake DNS"},
		{ID: "fake.records", Kind: providers.Solver, Brand: "Fake DNS with any record", Records: true},
		{ID: "fake.web", Kind: providers.Solver, Brand: "Fake web root", Challenge: providers.ChallengeHTTP},
		{ID: "fake.hook", Kind: providers.Target, Brand: "Fake target"},
		{ID: "fake.files", Kind: providers.Source, Brand: "Fake files of another client", OwnKey: true},
		{ID: "fake.acme", Kind: providers.Source, Brand: "Fake CA that revokes", NeedsSolver: true, Revocable: true},
		{ID: "fake.chat", Kind: providers.Notifier, Brand: "Fake chat"},
		{ID: "fake.spare", Kind: providers.Source, Brand: "Fake CA to fall back on", NeedsSolver: true, Challenges: []string{providers.ChallengeHTTP}, Backup: true},
	} {
		err := scope.Add(typ, func(context.Context, providers.Instance) (any, error) {
			f.fake.mu.Lock()
			defer f.fake.mu.Unlock()
			f.fake.opened++
			switch {
			case typ.Backup:
				return &fakeSpare{f}, nil
			case typ.OwnKey:
				return &fakeKeySource{f}, nil
			case typ.Revocable:
				return &fakeRevoker{fakeSource{f}}, nil
			case typ.Kind == providers.Source:
				return &fakeSource{f}, nil
			case typ.Challenge == providers.ChallengeHTTP:
				return &fakeFiles{f}, nil
			case typ.Records:
				return &fakeRecords{fakeSolver{f}}, nil
			case typ.Kind == providers.Solver:
				return &fakeSolver{f}, nil
			case typ.Kind == providers.Target:
				return &fakeTarget{f}, nil
			}
			return struct{}{}, nil
		})
		if err != nil {
			f.t.Fatal(err)
		}
	}
}

// provider creates an instance of a fake type.
func (f *fixture) provider(typ string) int64 {
	f.t.Helper()
	id, err := f.reg.Create(f.t.Context(), typ, typ, map[string]string{}, map[string]string{})
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

type fakeSource struct{ f *fixture }

func (s *fakeSource) Issue(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
	s.f.fake.mu.Lock()
	issue := s.f.fake.issue
	s.f.fake.mu.Unlock()
	if issue != nil {
		return issue(ctx, req)
	}
	return s.f.issueAll(ctx, req)
}

func (s *fakeSource) Close() error { return s.f.closed() }

func (s *fakeSource) RenewalInfo(_ context.Context, leaf *x509.Certificate) (contracts.RenewalInfo, error) {
	s.f.fake.mu.Lock()
	defer s.f.fake.mu.Unlock()
	s.f.fake.asked++
	if s.f.fake.renewal == nil {
		return contracts.RenewalInfo{}, contracts.ErrNoRenewalInfo
	}
	return s.f.fake.renewal(leaf)
}

// fakeRevoker is a fake CA that revokes too.
type fakeRevoker struct{ fakeSource }

func (s *fakeRevoker) Revoke(_ context.Context, leaf *x509.Certificate, reason contracts.RevocationReason) error {
	s.f.fake.mu.Lock()
	defer s.f.fake.mu.Unlock()
	if s.f.fake.revoke != nil {
		if err := s.f.fake.revoke(leaf, reason); err != nil {
			return err
		}
	}
	s.f.fake.revoked = append(s.f.fake.revoked, fmt.Sprintf("%x %d", leaf.SerialNumber, reason))
	return nil
}

// fakeSpare is a fake CA to fall back on: it issues whatever the fake
// source does, counting the certificates.
type fakeSpare struct{ f *fixture }

func (s *fakeSpare) Issue(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
	s.f.fake.mu.Lock()
	s.f.fake.spared++
	s.f.fake.mu.Unlock()
	return s.f.issueAll(ctx, req)
}

func (s *fakeSpare) Close() error { return s.f.closed() }

type fakeKeySource struct{ f *fixture }

func (s *fakeKeySource) Collect(ctx context.Context, req contracts.CollectRequest) (*contracts.Collected, error) {
	s.f.fake.mu.Lock()
	collect := s.f.fake.collect
	s.f.fake.mu.Unlock()
	return collect(ctx, req)
}

func (s *fakeKeySource) Close() error { return s.f.closed() }

// issueAll goes through the steps of an ACME issuance and issues the
// certificate.
func (f *fixture) issueAll(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
	req.Log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AcmeLogOrder, Params: map[string]any{"domains": strings.Join(req.Domains, ", ")}})
	if req.Solver != nil {
		for _, d := range req.Domains {
			req.Log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepChallenge, Key: i18n.AcmeLogPresent, Params: map[string]any{"domain": d}})
			if err := req.Solver.Present(ctx, contracts.Challenge{Domain: strings.TrimPrefix(d, "*.")}); err != nil {
				return nil, err
			}
		}
		for _, d := range req.Domains {
			req.Log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepCleanup, Key: i18n.AcmeLogCleanup, Params: map[string]any{"domain": d}})
			if err := req.Solver.CleanUp(ctx, contracts.Challenge{Domain: strings.TrimPrefix(d, "*.")}); err != nil {
				return nil, err
			}
		}
	}
	req.Log(contracts.LogLine{Level: contracts.OK, Step: contracts.StepDownload, Key: i18n.AcmeLogIssued})
	leaf := f.ca.Leaf(f.t, req.Key, f.now.Add(-time.Hour), f.now.Add(90*day), req.Domains...)
	return []*x509.Certificate{leaf, f.ca.Intermediate}, nil
}

func (f *fixture) closed() error {
	f.fake.mu.Lock()
	defer f.fake.mu.Unlock()
	f.fake.opened--
	return nil
}

func (f *fixture) failWith(err error) {
	f.fake.mu.Lock()
	defer f.fake.mu.Unlock()
	f.fake.issue = func(context.Context, contracts.IssueRequest) ([]*x509.Certificate, error) { return nil, err }
}

func (f *fixture) succeed() {
	f.fake.mu.Lock()
	defer f.fake.mu.Unlock()
	f.fake.issue = nil
}

type fakeSolver struct{ f *fixture }

func (s *fakeSolver) record(call string) {
	s.f.fake.mu.Lock()
	defer s.f.fake.mu.Unlock()
	s.f.fake.calls = append(s.f.fake.calls, call)
}

func (s *fakeSolver) Present(_ context.Context, ch contracts.Challenge) error {
	s.record("present " + ch.Domain)
	return nil
}

func (s *fakeSolver) CleanUp(_ context.Context, ch contracts.Challenge) error {
	s.record("cleanup " + ch.Domain)
	return nil
}

func (*fakeSolver) Wait() (time.Duration, time.Duration) { return time.Minute, time.Second }
func (s *fakeSolver) Close() error                       { return s.f.closed() }

// fakeFiles is a solver that validates over HTTP.
type fakeFiles struct{ f *fixture }

func (s *fakeFiles) PutFile(_ context.Context, file contracts.File) error {
	(&fakeSolver{s.f}).record("put " + file.Domain + file.Path)
	return nil
}

func (s *fakeFiles) RemoveFile(_ context.Context, file contracts.File) error {
	(&fakeSolver{s.f}).record("remove " + file.Domain + file.Path)
	return nil
}

func (s *fakeFiles) Close() error { return s.f.closed() }

// fakeRecords is a solver of DNS that adds records of any type too.
type fakeRecords struct{ fakeSolver }

func (s *fakeRecords) AddRecord(_ context.Context, r contracts.Record) (string, error) {
	s.record("add " + r.Type + " " + r.Name)
	return "ref", nil
}

func (s *fakeRecords) RemoveRecord(_ context.Context, r contracts.Record, ref string) error {
	s.record("remove " + r.Type + " " + r.Name + " " + ref)
	return nil
}

// issueNew adds a certificate issued by the fake CA through the fake DNS.
func (f *fixture) issueNew(domains ...string) CertificateBody {
	f.t.Helper()
	var c CertificateBody
	f.call(f.api.Post("/certificates/issue", map[string]any{
		"domains": domains, "sourceProvider": f.provider("fake.ca"), "solverProvider": f.provider("fake.dns"), "keyType": "ecdsa-p256",
	}), http.StatusCreated, &c)
	return c
}

// run runs the issuance job once, until no issuance is due.
func (f *fixture) run() {
	f.t.Helper()
	if err := f.svc.runIssuances(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) certificate(id int64) CertificateBody {
	f.t.Helper()
	var d struct {
		Certificate CertificateBody `json:"certificate"`
	}
	f.call(f.api.Get("/certificates/"+strconv.FormatInt(id, 10)), http.StatusOK, &d)
	return d.Certificate
}

type issuanceDetail struct {
	Issuance    IssuanceBody    `json:"issuance"`
	Certificate CertificateBody `json:"certificate"`
	Steps       []StepBody      `json:"steps"`
	Log         []LogLineBody   `json:"log"`
}

func (f *fixture) issuanceDetail(id int64) issuanceDetail {
	f.t.Helper()
	var d issuanceDetail
	f.call(f.api.Get("/issuances/"+strconv.FormatInt(id, 10)), http.StatusOK, &d)
	return d
}

func logKeys(d issuanceDetail) []i18n.Key {
	var keys []i18n.Key
	for _, l := range d.Log {
		keys = append(keys, l.Key)
	}
	return keys
}

func eventTypes(events []storedEvent) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.typ)
	}
	return out
}

func TestIssue(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("Example.COM.", "*.example.com", "example.com", bucher+".example.com")
	if strings.Join(c.Domains, ",") != "example.com,*.example.com,xn--bcher-kva.example.com" || c.Source != "provider" ||
		c.SourceProvider == 0 || c.SolverProvider == 0 || c.KeyType != "ecdsa-p256" || c.RenewBeforeDays != 30 ||
		c.State != "issuing" || c.Current != nil || c.Issuance == nil {
		t.Fatalf("added %+v", c)
	}
	if is := c.Issuance; is.Reason != "first" || is.State != "pending" || is.Attempts != 0 || is.NextAt == nil {
		t.Errorf("issuance %+v", is)
	}
	if f.triggered.Load() != 1 {
		t.Error("the runner was not started")
	}

	f.run()
	c = f.certificate(c.ID)
	if c.State != "ok" || c.Current == nil || c.Current.Origin != "issued" || c.Issuance.State != "done" ||
		c.Issuance.VersionID != c.Current.ID || c.Issuance.Attempts != 1 || c.Issuance.FinishedAt == nil {
		t.Fatalf("after the run: %+v", c)
	}
	if !slices.Equal(f.fake.calls, []string{
		"present example.com", "present example.com", "present xn--bcher-kva.example.com",
		"cleanup example.com", "cleanup example.com", "cleanup xn--bcher-kva.example.com",
	}) {
		t.Errorf("solver calls %v", f.fake.calls)
	}
	if f.fake.opened != 0 {
		t.Errorf("%d providers left open", f.fake.opened)
	}

	d := f.issuanceDetail(c.Issuance.ID)
	if !slices.Equal(logKeys(d), []i18n.Key{
		i18n.CertsLogKey, i18n.AcmeLogOrder, i18n.AcmeLogPresent, i18n.AcmeLogPresent, i18n.AcmeLogPresent,
		i18n.AcmeLogCleanup, i18n.AcmeLogCleanup, i18n.AcmeLogCleanup, i18n.AcmeLogIssued, i18n.CertsLogStored, i18n.CertsLogDone,
	}) {
		t.Errorf("log %v", logKeys(d))
	}
	if p := d.Log[0].Params; p["kind"] != "ecdsa" || p["size"] != "P-256" {
		t.Errorf("key line %+v", d.Log[0])
	}
	var steps []string
	for _, st := range d.Steps {
		steps = append(steps, st.Step+" "+st.State)
	}
	// The fake CA does not log a validate step, so a done issuance leaves it out.
	if strings.Join(steps, ", ") != "key done, order done, challenge done, cleanup done, download done" {
		t.Errorf("steps %v", steps)
	}
	if got := eventTypes(f.events()); !slices.Equal(got, []string{contracts.CertificateIssued}) {
		t.Errorf("events %v", got)
	}
}

func TestIssueDefaults(t *testing.T) {
	f := newFixture(t)
	var c CertificateBody
	f.call(f.api.Post("/certificates/issue", map[string]any{
		"domains": []string{"example.com"}, "sourceProvider": f.provider("fake.free"),
	}), http.StatusCreated, &c)
	if c.KeyType != "rsa-2048" || c.SolverProvider != 0 {
		t.Fatalf("added %+v", c)
	}
	f.run()
	c = f.certificate(c.ID)
	if c.State != "ok" || c.Current.KeyType != "rsa-2048" {
		t.Fatalf("issued %+v", c)
	}
	var steps []string
	for _, st := range f.issuanceDetail(c.Issuance.ID).Steps {
		steps = append(steps, st.Step)
	}
	if strings.Join(steps, ",") != "key,order,download" {
		t.Errorf("steps without a solver: %v", steps)
	}
}

func TestIssueChecks(t *testing.T) {
	f := newFixture(t)
	source, solver, target := f.provider("fake.ca"), f.provider("fake.dns"), f.provider("fake.hook")
	for name, tt := range map[string]struct {
		body   map[string]any
		fields []string
		codes  []i18n.Key
	}{
		"bad domains": {
			map[string]any{"domains": []string{"example.com", "exa mple.com", "a.*.example.com", "*"}, "sourceProvider": source, "solverProvider": solver},
			[]string{"body.domains[1]", "body.domains[2]", "body.domains[3]"},
			[]i18n.Key{i18n.CertsErrorBadDomain, i18n.CertsErrorBadDomain, i18n.CertsErrorBadDomain},
		},
		"no solver": {
			map[string]any{"domains": []string{"example.com"}, "sourceProvider": source},
			[]string{"body.solverProvider"}, []i18n.Key{i18n.KernelErrorFieldRequired},
		},
		"wrong kinds": {
			map[string]any{"domains": []string{"example.com"}, "sourceProvider": target, "solverProvider": source},
			[]string{"body.sourceProvider", "body.solverProvider"}, []i18n.Key{i18n.CertsErrorWrongProvider, i18n.CertsErrorWrongProvider},
		},
		"unknown": {
			map[string]any{"domains": []string{"example.com"}, "sourceProvider": 999, "solverProvider": solver},
			[]string{"body.sourceProvider"}, []i18n.Key{i18n.KernelErrorNotFound},
		},
	} {
		e := f.apiError(f.api.Post("/certificates/issue", tt.body), http.StatusUnprocessableEntity)
		var fields []string
		var codes []i18n.Key
		for _, fe := range e.Fields {
			fields, codes = append(fields, fe.Location), append(codes, i18n.Key(fe.Code))
		}
		if !slices.Equal(fields, tt.fields) || !slices.Equal(codes, tt.codes) {
			t.Errorf("%s: fields %v %v", name, fields, codes)
		}
	}
	var n int
	if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM certs_certificates").Scan(&n); err != nil || n != 0 {
		t.Errorf("%d certificates added, %v", n, err)
	}
}

func TestIssueChallenges(t *testing.T) {
	f := newFixture(t)
	ca, vendor, dns, web := f.provider("fake.ca"), f.provider("fake.vendor"), f.provider("fake.dns"), f.provider("fake.web")
	for name, tt := range map[string]struct {
		domains        []string
		source, solver int64
		code           i18n.Key
	}{
		"dns for a source of http": {[]string{"example.com"}, vendor, dns, i18n.CertsErrorSolverChallenge},
		"http for a source of dns": {[]string{"example.com"}, ca, web, i18n.CertsErrorSolverChallenge},
		"wildcard over http":       {[]string{"example.com", "*.example.com"}, vendor, web, i18n.CertsErrorWildcardHttp},
		"an address over dns":      {[]string{"example.com", "192.0.2.1"}, ca, dns, i18n.CertsErrorIpDns},
	} {
		e := f.apiError(f.api.Post("/certificates/issue", map[string]any{"domains": tt.domains, "sourceProvider": tt.source, "solverProvider": tt.solver}),
			http.StatusUnprocessableEntity)
		if len(e.Fields) != 1 || e.Fields[0].Location != "body.solverProvider" || e.Fields[0].Code != string(tt.code) {
			t.Errorf("%s: %+v", name, e.Fields)
		}
	}

	// The solver over HTTP comes as Files.
	var got contracts.IssueRequest
	f.fake.issue = func(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
		got = req
		if err := req.Files.PutFile(ctx, contracts.File{Domain: req.Domains[0], Path: "/.well-known/x"}); err != nil {
			return nil, err
		}
		return f.issueAll(ctx, req)
	}
	var c CertificateBody
	f.call(f.api.Post("/certificates/issue", map[string]any{"domains": []string{"example.com"}, "sourceProvider": vendor, "solverProvider": web}),
		http.StatusCreated, &c)
	f.run()
	if got.Files == nil || got.Solver != nil || f.certificate(c.ID).State != "ok" || !slices.Equal(f.fake.calls, []string{"put example.com/.well-known/x"}) {
		t.Errorf("request %+v, calls %v", got, f.fake.calls)
	}

	// A solver of DNS that adds any record comes as Records too, for a
	// source that asks for records; a plain one, for a CA, does not.
	f.fake.calls = nil
	f.fake.issue = func(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
		got = req
		if req.Records != nil {
			if _, err := req.Records.AddRecord(ctx, contracts.Record{Name: "_x." + req.Domains[0], Type: "CNAME", Value: "v"}); err != nil {
				return nil, err
			}
		}
		req.Solver = nil
		return f.issueAll(ctx, req)
	}
	f.call(f.api.Post("/certificates/issue", map[string]any{"domains": []string{"vendor.example.com"}, "sourceProvider": vendor, "solverProvider": f.provider("fake.records")}),
		http.StatusCreated, &c)
	f.run()
	if got.Records == nil || got.Solver == nil || f.certificate(c.ID).State != "ok" || !slices.Equal(f.fake.calls, []string{"add CNAME _x.vendor.example.com"}) {
		t.Errorf("request %+v, calls %v", got, f.fake.calls)
	}
	f.call(f.api.Post("/certificates/issue", map[string]any{"domains": []string{"ca.example.com"}, "sourceProvider": ca, "solverProvider": dns}),
		http.StatusCreated, &c)
	f.run()
	if got.Records != nil || got.Solver == nil || f.certificate(c.ID).State != "ok" {
		t.Errorf("request %+v", got)
	}
	if f.fake.opened != 0 {
		t.Errorf("%d providers left open", f.fake.opened)
	}
}

func TestIssueRetries(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.failWith(errors.New("connection refused"))
	f.run()

	c = f.certificate(c.ID)
	is := c.Issuance
	if c.State != "failed" || is.State != "pending" || is.Attempts != 1 || is.Error == nil || is.Error.Key != i18n.CertsErrorIssueFailed {
		t.Fatalf("after a failed attempt: %+v %+v", c, is)
	}
	if wait := is.NextAt.Sub(f.now); wait < retryBase/2 || wait > retryBase {
		t.Errorf("retry in %v", wait)
	}
	d := f.issuanceDetail(is.ID)
	last := d.Log[len(d.Log)-2]
	if last.Level != "error" || last.Step != "key" || last.Detail != "connection refused" ||
		d.Log[len(d.Log)-1].Key != i18n.CertsLogRetry {
		t.Errorf("log %+v", d.Log)
	}
	if st := d.Steps[0]; st.Step != "key" || st.State != "failed" {
		t.Errorf("steps %+v", d.Steps)
	}
	events := f.events()
	if len(events) != 1 || events[0].typ != contracts.CertificateIssueFailed {
		t.Fatalf("events %v", eventTypes(events))
	}
	if e := events[0].data.Issuance; e == nil || e.ID != is.ID || e.Attempt != 1 || e.Error.Key != i18n.CertsErrorIssueFailed || !e.Retry.Equal(*is.NextAt) {
		t.Errorf("event %+v", events[0].data)
	}

	// Not due yet.
	f.run()
	if got := f.certificate(c.ID).Issuance.Attempts; got != 1 {
		t.Errorf("retried early: %d attempts", got)
	}
	f.now = *is.NextAt
	f.succeed()
	f.run()
	c = f.certificate(c.ID)
	if c.State != "ok" || c.Issuance.State != "done" || c.Issuance.Attempts != 2 || c.Issuance.Error != nil {
		t.Errorf("after the retry: %+v %+v", c, c.Issuance)
	}
	if keys := logKeys(f.issuanceDetail(is.ID)); !slices.Contains(keys, i18n.CertsLogAttempt) {
		t.Errorf("no attempt line: %v", keys)
	}
}

func TestIssueGivesUp(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.failWith(&contracts.Problem{Key: i18n.AcmeErrorEabRequired})
	const maxAttempts = defaultRetries + 1
	for range maxAttempts {
		f.run()
		is := f.certificate(c.ID).Issuance
		if is.NextAt != nil {
			f.now = *is.NextAt
		}
	}
	c = f.certificate(c.ID)
	if is := c.Issuance; c.State != "failed" || is.State != "failed" || is.Attempts != maxAttempts ||
		is.FinishedAt == nil || is.Error.Key != i18n.AcmeErrorEabRequired {
		t.Fatalf("after %d attempts: %+v %+v", maxAttempts, c, c.Issuance)
	}
	events := f.events()
	if len(events) != maxAttempts || !events[len(events)-1].data.Issuance.Retry.IsZero() || events[0].data.Issuance.Retry.IsZero() {
		t.Errorf("events: %d", len(events))
	}
	if keys := logKeys(f.issuanceDetail(c.Issuance.ID)); keys[len(keys)-1] != i18n.CertsLogGaveUp {
		t.Errorf("log ends with %v", keys[len(keys)-1])
	}

	// A person can start again.
	f.succeed()
	var is IssuanceBody
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/issue"), http.StatusAccepted, &is)
	if is.Reason != "first" || is.State != "pending" || is.ID == c.Issuance.ID {
		t.Errorf("manual issuance %+v", is)
	}
	f.run()
	if got := f.certificate(c.ID); got.State != "ok" {
		t.Errorf("after the manual issuance: %s", got.State)
	}
}

func TestIssuePermanent(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.failWith(retry.Permanent(&contracts.Problem{Key: i18n.AcmeErrorEabRequired, Err: errors.New("externalAccountRequired")}))
	f.run()
	c = f.certificate(c.ID)
	if is := c.Issuance; is.State != "failed" || is.Attempts != 1 || is.Error.Key != i18n.AcmeErrorEabRequired {
		t.Fatalf("issuance %+v", is)
	}
	d := f.issuanceDetail(c.Issuance.ID)
	if l := d.Log[len(d.Log)-2]; l.Key != i18n.AcmeErrorEabRequired || l.Detail != "externalAccountRequired" {
		t.Errorf("error line %+v", l)
	}
}

func TestManualIssue(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	path := "/certificates/" + strconv.FormatInt(c.ID, 10) + "/issue"

	// While the first issuance waits, asking again returns it.
	var is IssuanceBody
	f.call(f.api.Post(path), http.StatusAccepted, &is)
	if is.ID != c.Issuance.ID {
		t.Errorf("a second issuance %d", is.ID)
	}
	f.run()

	// A retry waiting is made due now.
	f.failWith(errors.New("timeout"))
	f.call(f.api.Post(path), http.StatusAccepted, &is)
	if is.Reason != "manual" {
		t.Errorf("reason %s", is.Reason)
	}
	f.run()
	waiting := f.certificate(c.ID).Issuance
	if waiting.State != "pending" || waiting.Attempts != 1 {
		t.Fatalf("issuance %+v", waiting)
	}
	f.call(f.api.Post(path), http.StatusAccepted, &is)
	if is.ID != waiting.ID || !is.NextAt.Equal(f.now) {
		t.Errorf("the retry was not made due: %+v", is)
	}

	// An uploaded certificate has no source.
	up := f.add(f.leaf(90, "upload.example.com"))
	if e := f.apiError(f.api.Post("/certificates/"+strconv.FormatInt(up.ID, 10)+"/issue"), http.StatusConflict); e.Code != string(i18n.CertsErrorNoSource) {
		t.Errorf("error %+v", e)
	}
	f.apiError(f.api.Post("/certificates/999/issue"), http.StatusNotFound)
}

func TestIssuanceList(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.run()
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)
	f.call(f.api.Post(path+"/issue"), http.StatusAccepted, nil)
	f.run()
	var list struct {
		Issuances []IssuanceBody `json:"issuances"`
	}
	f.call(f.api.Get(path+"/issuances"), http.StatusOK, &list)
	if len(list.Issuances) != 2 || list.Issuances[0].Reason != "manual" || list.Issuances[1].Reason != "first" {
		t.Errorf("issuances %+v", list.Issuances)
	}
	f.call(f.api.Get(path+"/issuances?limit=1"), http.StatusOK, &list)
	if len(list.Issuances) != 1 {
		t.Errorf("limit: %d", len(list.Issuances))
	}
	f.apiError(f.api.Get("/certificates/999/issuances"), http.StatusNotFound)
	f.apiError(f.api.Get("/issuances/999"), http.StatusNotFound)
}

func TestIssueResumes(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	// An attempt claimed by a JEC that then stopped.
	if claimed, err := f.svc.claim(t.Context()); err != nil || claimed == nil {
		t.Fatal(claimed, err)
	}
	f.run()
	c = f.certificate(c.ID)
	if c.State != "ok" || c.Issuance.Attempts != 2 {
		t.Fatalf("after the restart: %+v %+v", c, c.Issuance)
	}
	if keys := logKeys(f.issuanceDetail(c.Issuance.ID)); keys[0] != i18n.CertsLogInterrupted {
		t.Errorf("log %v", keys)
	}
}

// vendor makes the fake source act like a vendor that takes time: it waits
// in an attempt for each of states, in order, then issues. It records the
// key and state of each call.
func (f *fixture) vendor(waits ...time.Duration) *[]contracts.IssueRequest {
	calls := &[]contracts.IssueRequest{}
	f.fake.mu.Lock()
	defer f.fake.mu.Unlock()
	f.fake.issue = func(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
		*calls = append(*calls, req)
		n := len(req.State)
		if n < len(waits) {
			step := []contracts.Step{contracts.StepOrder, contracts.StepValidate}[min(n, 1)]
			req.Log(contracts.LogLine{Level: contracts.Info, Step: step, Key: i18n.AcmeLogOrder})
			return nil, &contracts.Wait{State: append(req.State, 'w'), After: waits[n]}
		}
		return f.issueAll(ctx, req)
	}
	return calls
}

// sealed tells whether issuance id holds a key and a source state.
func (f *fixture) sealed(id int64) (key, state bool) {
	f.t.Helper()
	if err := f.db.QueryRowContext(f.t.Context(), "SELECT sealed_key IS NOT NULL, sealed_state IS NOT NULL FROM certs_issuances WHERE id = ?", id).
		Scan(&key, &state); err != nil {
		f.t.Fatal(err)
	}
	return key, state
}

func stepStates(d issuanceDetail) string {
	var out []string
	for _, st := range d.Steps {
		out = append(out, st.Step+" "+st.State)
	}
	return strings.Join(out, ", ")
}

func TestIssueWaits(t *testing.T) {
	f := newFixture(t)
	calls := f.vendor(5*time.Minute, 0)
	c := f.issueNew("example.com")
	f.run()

	c = f.certificate(c.ID)
	is := c.Issuance
	if c.State != "issuing" || is.State != "waiting" || is.Attempts != 1 || is.Error != nil || !is.NextAt.Equal(f.now.Add(5*time.Minute)) {
		t.Fatalf("after the first attempt: %+v %+v", c, is)
	}
	if key, state := f.sealed(is.ID); !key || !state {
		t.Errorf("sealed key %v, state %v", key, state)
	}
	if got := stepStates(f.issuanceDetail(is.ID)); got != "key done, order running, challenge pending, validate pending, cleanup pending, download pending" {
		t.Errorf("steps %s", got)
	}

	// Not due yet; then due, waiting again for at least shortestWait.
	f.run()
	f.now = *is.NextAt
	f.run()
	is = f.certificate(c.ID).Issuance
	if len(*calls) != 2 || is.State != "waiting" || is.Attempts != 1 || !is.NextAt.Equal(f.now.Add(shortestWait)) {
		t.Fatalf("after the second call (%d): %+v", len(*calls), is)
	}
	if got := stepStates(f.issuanceDetail(is.ID)); got != "key done, order done, challenge pending, validate running, cleanup pending, download pending" {
		t.Errorf("steps %s", got)
	}

	f.now = *is.NextAt
	f.run()
	c = f.certificate(c.ID)
	if c.State != "ok" || c.Issuance.State != "done" || c.Issuance.Attempts != 1 || len(*calls) != 3 {
		t.Fatalf("after the third call: %+v %+v", c, c.Issuance)
	}
	first := (*calls)[0]
	for i, call := range *calls {
		if !call.Key.Public().(interface{ Equal(crypto.PublicKey) bool }).Equal(first.Key.Public()) {
			t.Errorf("call %d got another key", i)
		}
		if string(call.State) != strings.Repeat("w", i) {
			t.Errorf("call %d got state %q", i, call.State)
		}
	}
	if key, state := f.sealed(c.Issuance.ID); key || state {
		t.Errorf("done, but still sealed: key %v, state %v", key, state)
	}
	keys := logKeys(f.issuanceDetail(c.Issuance.ID))
	if slices.Contains(keys, i18n.CertsLogAttempt) || slices.Index(keys, i18n.CertsLogKey) != 0 || keys[len(keys)-1] != i18n.CertsLogDone {
		t.Errorf("log %v", keys)
	}
}

func TestIssueTakesOver(t *testing.T) {
	f := newFixture(t)
	f.vendor(time.Hour)
	c := f.issueNew("example.com")
	f.run()

	// The source then fails for good, with the order still on its side.
	f.failWith(retry.Permanent(&contracts.Problem{Key: i18n.AcmeErrorEabRequired}))
	f.now = *f.certificate(c.ID).Issuance.NextAt
	f.run()
	old := f.certificate(c.ID).Issuance
	if old.State != "failed" {
		t.Fatalf("issuance %+v", old)
	}
	if key, state := f.sealed(old.ID); !key || !state {
		t.Fatalf("the failed issuance keeps key %v, state %v", key, state)
	}

	// The next issuance goes on with that order.
	calls := f.vendor(time.Hour)
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/issue"), http.StatusAccepted, nil)
	if key, state := f.sealed(old.ID); key || state {
		t.Errorf("the failed issuance still holds key %v, state %v", key, state)
	}
	f.run()
	c = f.certificate(c.ID)
	if c.State != "ok" || len(*calls) != 1 || string((*calls)[0].State) != "w" {
		t.Fatalf("after the takeover: %+v, %d calls", c, len(*calls))
	}
	d := f.issuanceDetail(c.Issuance.ID)
	if keys := logKeys(d); keys[0] != i18n.CertsLogTakeOver || slices.Contains(keys, i18n.CertsLogKey) {
		t.Errorf("log %v", keys)
	}
	if got := stepStates(d); !strings.HasPrefix(got, "key done, ") {
		t.Errorf("steps %s", got)
	}
}

func TestIssueRestart(t *testing.T) {
	f := newFixture(t)
	f.vendor(time.Hour)
	c := f.issueNew("example.com")
	f.run()

	// The source finds its order spent: the issuance fails, and keeps
	// neither the key nor the order.
	f.failWith(contracts.Restart(retry.Permanent(&contracts.Problem{Key: i18n.AcmeErrorEabRequired})))
	f.now = *f.certificate(c.ID).Issuance.NextAt
	f.run()
	old := f.certificate(c.ID).Issuance
	if key, state := f.sealed(old.ID); old.State != "failed" || key || state {
		t.Fatalf("issuance %+v keeps key %v, state %v", old, key, state)
	}

	// The next issuance starts afresh.
	calls := f.vendor()
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/issue"), http.StatusAccepted, nil)
	f.run()
	c = f.certificate(c.ID)
	if c.State != "ok" || len(*calls) != 1 || (*calls)[0].State != nil {
		t.Fatalf("after the restart: %+v, %d calls", c, len(*calls))
	}
	if keys := logKeys(f.issuanceDetail(c.Issuance.ID)); slices.Contains(keys, i18n.CertsLogTakeOver) || !slices.Contains(keys, i18n.CertsLogKey) {
		t.Errorf("log %v", keys)
	}

	// A restart that may be retried retries afresh, with a new key.
	f.vendor(time.Hour)
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/issue"), http.StatusAccepted, nil)
	f.run()
	f.failWith(contracts.Restart(errors.New("the order is gone")))
	f.now = *f.certificate(c.ID).Issuance.NextAt
	f.run()
	is := f.certificate(c.ID).Issuance
	if key, state := f.sealed(is.ID); is.State != "pending" || key || state {
		t.Errorf("retry %+v keeps key %v, state %v", is, key, state)
	}
}

func TestIssueKeepsState(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")

	// The attempt placed an order before it failed: the next one goes on
	// with it.
	f.failWith(contracts.KeepState(errors.New("timeout"), []byte("order")))
	f.run()
	is := f.certificate(c.ID).Issuance
	if key, state := f.sealed(is.ID); is.State != "pending" || !key || !state {
		t.Fatalf("retry %+v keeps key %v, state %v", is, key, state)
	}
	calls := f.vendor()
	f.now = *is.NextAt
	f.run()
	if c = f.certificate(c.ID); c.State != "ok" || len(*calls) != 1 || string((*calls)[0].State) != "order" {
		t.Fatalf("after the retry: %+v, %d calls", c, len(*calls))
	}

	// Failing for good, the issuance keeps the key with the order, for the
	// next issuance; a restart keeps neither.
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/issue"), http.StatusAccepted, nil)
	f.failWith(contracts.KeepState(retry.Permanent(errors.New("refused")), []byte("order")))
	f.run()
	is = f.certificate(c.ID).Issuance
	if key, state := f.sealed(is.ID); is.State != "failed" || !key || !state {
		t.Errorf("failed %+v keeps key %v, state %v", is, key, state)
	}
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/issue"), http.StatusAccepted, nil)
	f.failWith(contracts.Restart(contracts.KeepState(retry.Permanent(errors.New("refused")), []byte("order"))))
	f.run()
	is = f.certificate(c.ID).Issuance
	if key, state := f.sealed(is.ID); is.State != "failed" || key || state {
		t.Errorf("restarted %+v keeps key %v, state %v", is, key, state)
	}
}

func TestIssueFailureDropsKey(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.failWith(errors.New("timeout"))
	f.run()
	if key, state := f.sealed(f.certificate(c.ID).Issuance.ID); !key || state {
		t.Fatalf("a retry keeps the key: key %v, state %v", key, state)
	}
	f.failWith(retry.Permanent(errors.New("refused")))
	f.now = *f.certificate(c.ID).Issuance.NextAt
	f.run()
	is := f.certificate(c.ID).Issuance
	if key, _ := f.sealed(is.ID); is.State != "failed" || key {
		t.Errorf("failed with no order: %+v, key %v", is, key)
	}
}

func TestIssueStops(t *testing.T) {
	f := newFixture(t)
	started := make(chan struct{})
	f.fake.issue = func(ctx context.Context, _ contracts.IssueRequest) ([]*x509.Certificate, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	// JEC stopping leaves the issuance running, to resume after the restart.
	c := f.issueNew("example.com")
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error)
	go func() { done <- f.svc.runIssuances(ctx) }()
	<-started
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if is := f.certificate(c.ID).Issuance; is.State != "running" || is.Error != nil {
		t.Errorf("after the stop: %+v", is)
	}

	// Deleting the certificate stops its attempt, with no failure told.
	started = make(chan struct{})
	go func() { done <- f.svc.runIssuances(t.Context()) }()
	<-started
	f.call(f.api.Delete("/certificates/"+strconv.FormatInt(c.ID, 10)), http.StatusNoContent, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := eventTypes(f.events()); !slices.Equal(got, []string{contracts.CertificateDeleted}) {
		t.Errorf("events %v", got)
	}
}

func TestIssueConcurrency(t *testing.T) {
	f := newFixture(t)
	entered := make(chan struct{}, 5)
	release := make(chan struct{})
	f.fake.issue = func(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
		entered <- struct{}{}
		<-release
		return f.issueAll(ctx, req)
	}
	for i := range 5 {
		f.issueNew("site" + strconv.Itoa(i) + ".example.com")
	}
	done := make(chan error)
	go func() { done <- f.svc.runIssuances(t.Context()) }()
	for range maxRunning {
		<-entered
	}
	select {
	case <-entered:
		t.Errorf("more than %d attempts at once", maxRunning)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	certs, err := f.svc.list(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range certs {
		if c.state(f.now) != stateOK {
			t.Errorf("%s: %s", c.Domains[0], c.state(f.now))
		}
	}
}

func TestRenew(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.run()
	up := f.add(f.leaf(10, "upload.example.com"))

	// Outside the renewal window: nothing to do.
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.certificate(c.ID).Issuance.Reason; got != "first" {
		t.Fatalf("renewed early: %s", got)
	}

	f.now = f.now.Add(61 * day)
	triggered := f.triggered.Load()
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	is := f.certificate(c.ID).Issuance
	if is.Reason != "renewal" || is.State != "pending" || f.triggered.Load() != triggered+1 {
		t.Fatalf("renewal %+v", is)
	}
	if f.certificate(up.ID).Issuance != nil {
		t.Error("an uploaded certificate was renewed")
	}
	// Under way: not started twice.
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.certificate(c.ID).Issuance.ID; got != is.ID {
		t.Error("a second renewal started")
	}

	// A renewal that failed for good waits a day before the next.
	f.failWith(retry.Permanent(errors.New("rejected")))
	f.run()
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.certificate(c.ID).Issuance; got.ID != is.ID || got.State != "failed" {
		t.Fatalf("renewed again at once: %+v", got)
	}
	f.now = f.now.Add(failedPause)
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.certificate(c.ID).Issuance; got.ID == is.ID || got.Reason != "renewal" {
		t.Errorf("no renewal a day later: %+v", got)
	}
}

func TestRenewPaused(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.run()
	paused := true
	f.call(f.api.Patch("/certificates/"+strconv.FormatInt(c.ID, 10), map[string]any{"paused": paused}), http.StatusOK, nil)
	got := f.certificate(c.ID)
	if !got.Paused || got.RenewAt != nil {
		t.Fatalf("paused: %+v", got)
	}

	// Due, but paused: not renewed, though someone may still ask.
	f.now = f.now.Add(61 * day)
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.certificate(c.ID).Issuance.Reason; got != "first" {
		t.Fatalf("renewed while paused: %s", got)
	}
	f.call(f.api.Patch("/certificates/"+strconv.FormatInt(c.ID, 10), map[string]any{"paused": false}), http.StatusOK, nil)
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.certificate(c.ID); got.Issuance.Reason != "renewal" || got.Paused || got.RenewAt == nil {
		t.Fatalf("not renewed once resumed: %+v", got)
	}
}

func TestFailedStateEnds(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.failWith(retry.Permanent(errors.New("rejected")))
	f.run()
	if got := f.certificate(c.ID).State; got != "failed" {
		t.Fatalf("state %s", got)
	}
	// A version uploaded since ends the failed state.
	f.now = f.now.Add(time.Minute)
	u := f.leaf(90, "example.com")
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/versions", map[string]any{
		"certificate": u.cert, "privateKey": u.key,
	}), http.StatusCreated, nil)
	if got := f.certificate(c.ID).State; got != "ok" {
		t.Errorf("state after the upload: %s", got)
	}
}

func TestProviderInUse(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	var inUse *providers.InUseError
	if err := f.reg.Delete(t.Context(), c.SourceProvider); !errors.As(err, &inUse) || inUse.Module != "certs" {
		t.Errorf("deleting the source: %v", err)
	}
	if err := f.reg.Delete(t.Context(), c.SolverProvider); !errors.As(err, &inUse) {
		t.Errorf("deleting the solver: %v", err)
	}
	f.call(f.api.Delete("/certificates/"+strconv.FormatInt(c.ID, 10)), http.StatusNoContent, nil)
	if err := f.reg.Delete(t.Context(), c.SourceProvider); err != nil {
		t.Errorf("deleting the source once unused: %v", err)
	}
}

// Internationalized names, spelled out so that the source stays ASCII.
var (
	bucher        = "b" + string(rune(0xfc)) + "cher"                              // German for books
	example, test = string([]rune{0x4f8b, 0x5b50}), string([]rune{0x6d4b, 0x8bd5}) // Chinese
)

func TestNormalizeDomain(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"Example.com", "example.com"},
		{" example.com. ", "example.com"},
		{"*.Example.com", "*.example.com"},
		{bucher + ".de", "xn--bcher-kva.de"},
		{"xn--bcher-kva.de", "xn--bcher-kva.de"},
		{example + "." + test, "xn--fsqu00a.xn--0zwm56d"}, // the IANA test domain in Chinese
		{"a_b.example.com", ""},
		{"*.*.example.com", ""},
		{"a.*.example.com", ""},
		{"*", ""},
		{"", ""},
		{"-a.example.com", ""},
		{strings.Repeat("a", 64) + ".com", ""},
		{"192.0.2.1", "192.0.2.1"},
		{"[2001:DB8::0:1]", "2001:db8::1"},
		{"::ffff:192.0.2.1", "192.0.2.1"},
		{"fe80::1%eth0", ""},
		{"*.192.0.2.1", ""},
	} {
		got, ok := normalizeDomain(tt.in)
		if got != tt.want || ok != (tt.want != "") {
			t.Errorf("normalizeDomain(%q) = %q, %v; want %q", tt.in, got, ok, tt.want)
		}
	}
	out, bad := normalizeDomains([]string{"a.com", "A.com", "bad name", "b.com"})
	if !slices.Equal(out, []string{"a.com", "b.com"}) || !slices.Equal(bad, []int{2}) {
		t.Errorf("normalizeDomains: %v, %v", out, bad)
	}
}

func TestKeySource(t *testing.T) {
	f := newFixture(t)
	held := f.leaf(60, "example.com") // what the other client keeps
	var asked []contracts.CollectRequest
	f.fake.mu.Lock()
	f.fake.collect = func(_ context.Context, req contracts.CollectRequest) (*contracts.Collected, error) {
		asked = append(asked, req)
		b, err := cryptox.ParseBundle([]byte(held.cert), []byte(held.key))
		if err != nil {
			return nil, err
		}
		if req.Current == cryptox.Fingerprint(b.Leaf) {
			return nil, &contracts.Wait{After: 6 * time.Hour}
		}
		return &contracts.Collected{Chain: append([]*x509.Certificate{b.Leaf}, b.Chain...), Key: b.Key}, nil
	}
	f.fake.mu.Unlock()

	var c CertificateBody
	f.call(f.api.Post("/certificates/issue", map[string]any{
		"domains": []string{"example.com"}, "sourceProvider": f.provider("fake.files"), "keyType": "rsa-4096",
	}), http.StatusCreated, &c)
	if c.KeyType != "" {
		t.Errorf("a key type of its own: %q", c.KeyType)
	}
	f.run()
	c = f.certificate(c.ID)
	if c.Current == nil || c.Current.KeyType != "ecdsa-p256" || c.State != "ok" || c.Issuance.State != "done" {
		t.Fatalf("first version: %+v", c)
	}
	first := c.Current.Fingerprint

	// Due, but the other client has yet to renew it: the issuance waits.
	f.now = *c.RenewAt
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.run()
	if is := f.certificate(c.ID).Issuance; is.State != "waiting" || is.Reason != "renewal" {
		t.Fatalf("nothing new: %+v", is)
	}
	if last := asked[len(asked)-1]; last.Current != first || !last.CurrentEnd.Equal(c.Current.NotAfter) {
		t.Errorf("asked with %+v", last)
	}

	// It renewed: the next look takes the new one in.
	held = f.leaf(90, "example.com")
	f.now = f.now.Add(7 * time.Hour)
	f.run()
	c = f.certificate(c.ID)
	if c.Current.Fingerprint == first || c.Issuance.State != "done" {
		t.Errorf("after the renewal: %+v", c)
	}
}

func TestKeySourceKeepsState(t *testing.T) {
	f := newFixture(t)
	held := f.leaf(60, "example.com")
	var states []string
	f.fake.mu.Lock()
	f.fake.collect = func(_ context.Context, req contracts.CollectRequest) (*contracts.Collected, error) {
		// A vendor that makes the key: it orders first, then waits for
		// the order, which the state names.
		states = append(states, string(req.State))
		if req.State == nil {
			return nil, &contracts.Wait{State: []byte("order-1"), After: time.Minute}
		}
		b, err := cryptox.ParseBundle([]byte(held.cert), []byte(held.key))
		if err != nil {
			return nil, err
		}
		return &contracts.Collected{Chain: append([]*x509.Certificate{b.Leaf}, b.Chain...), Key: b.Key}, nil
	}
	f.fake.mu.Unlock()

	var c CertificateBody
	f.call(f.api.Post("/certificates/issue", map[string]any{"domains": []string{"example.com"}, "sourceProvider": f.provider("fake.files")}), http.StatusCreated, &c)
	f.run()
	is := f.certificate(c.ID).Issuance
	if _, state := f.sealed(is.ID); is.State != "waiting" || !state {
		t.Fatalf("after ordering: %+v, state %v", is, state)
	}
	f.now = *is.NextAt
	f.run()
	if c = f.certificate(c.ID); c.State != "ok" || !slices.Equal(states, []string{"", "order-1"}) {
		t.Errorf("after the order: %+v, states %q", c, states)
	}
	keys := logKeys(f.issuanceDetail(c.Issuance.ID))
	if n := len(slices.DeleteFunc(slices.Clone(keys), func(k i18n.Key) bool { return k != i18n.CertsLogKeyFromSource })); n != 1 {
		t.Errorf("the key from the source logged %d times: %v", n, keys)
	}
}

func TestSetDomains(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	path := "/certificates/" + strconv.FormatInt(c.ID, 10) + "/domains"
	// Not while it is being issued.
	if e := f.apiError(f.api.Put(path, map[string]any{"domains": []string{"example.com", "www.example.com"}}), http.StatusConflict); e.Code != string(i18n.CertsErrorIssuingNow) {
		t.Errorf("while issuing: %+v", e)
	}
	f.run()
	first := f.certificate(c.ID).Current.ID

	// The same domains change nothing.
	f.call(f.api.Put(path, map[string]any{"domains": []string{"Example.com."}}), http.StatusOK, nil)
	if is := f.certificate(c.ID).Issuance; is.State != "done" {
		t.Errorf("the same domains started %+v", is)
	}
	// Others are checked, then issued at once.
	e := f.apiError(f.api.Put(path, map[string]any{"domains": []string{"example.com", "bad name", "192.0.2.1"}}), http.StatusUnprocessableEntity)
	if len(e.Fields) != 2 || e.Fields[0].Location != "body.domains[1]" || e.Fields[1].Code != string(i18n.CertsErrorIpDns) {
		t.Errorf("bad domains: %+v", e.Fields)
	}
	var got CertificateBody
	f.call(f.api.Put(path, map[string]any{"domains": []string{"example.com", "WWW.example.com"}}), http.StatusOK, &got)
	if !slices.Equal(got.Domains, []string{"example.com", "www.example.com"}) || got.Issuance == nil || got.Issuance.Reason != "manual" || got.State != "issuing" {
		t.Fatalf("after = %+v", got)
	}
	f.run()
	if got := f.certificate(c.ID); got.Current.ID == first || !slices.Contains(got.Current.Names, "www.example.com") {
		t.Errorf("issued %+v", got.Current)
	}

	// An upload takes its domains from its file.
	up := f.add(f.leaf(60, "upload.example.com"))
	if e := f.apiError(f.api.Put("/certificates/"+strconv.FormatInt(up.ID, 10)+"/domains", map[string]any{"domains": []string{"other.example.com"}}), http.StatusConflict); e.Code != string(i18n.CertsErrorNoSource) {
		t.Errorf("upload: %+v", e)
	}
	f.apiError(f.api.Put("/certificates/999/domains", map[string]any{"domains": []string{"x.example.com"}}), http.StatusNotFound)
}

func TestReuseKey(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	f.run()
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)
	spki := func() string {
		t.Helper()
		var d struct {
			Details struct {
				SPKISHA256 string `json:"spkiSha256"`
			} `json:"details"`
		}
		f.call(f.api.Get(path), http.StatusOK, &d)
		return d.Details.SPKISHA256
	}
	first := spki()
	issueNow := func() {
		t.Helper()
		f.call(f.api.Post(path+"/issue"), http.StatusAccepted, nil)
		f.run()
	}

	// Kept: the same key, said in the log.
	var got CertificateBody
	f.call(f.api.Patch(path, map[string]any{"reuseKey": true}), http.StatusOK, &got)
	if !got.ReuseKey {
		t.Fatalf("not kept: %+v", got)
	}
	issueNow()
	if spki() != first {
		t.Error("the key changed while kept")
	}
	// Not kept any more: a new one.
	var off CertificateBody
	f.call(f.api.Patch(path, map[string]any{"reuseKey": false}), http.StatusOK, &off)
	issueNow()
	if spki() == first || off.ReuseKey {
		t.Error("the key stayed")
	}
}

func TestNotifyToChecked(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("example.com")
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)
	if e := f.apiError(f.api.Patch(path, map[string]any{"notifyTo": []int64{999, f.provider("fake.dns")}}), http.StatusUnprocessableEntity); len(e.Fields) != 2 ||
		e.Fields[0].Location != "body.notifyTo[0]" || e.Fields[1].Code != string(i18n.CertsErrorWrongProvider) {
		t.Errorf("not notifiers: %+v", e.Fields)
	}
	chat := f.provider("fake.chat")
	var got CertificateBody
	f.call(f.api.Patch(path, map[string]any{"notifyTo": []int64{chat, chat}}), http.StatusOK, &got)
	if !slices.Equal(got.NotifyTo, []int64{chat}) {
		t.Errorf("one: %+v", got.NotifyTo)
	}
	var all CertificateBody
	f.call(f.api.Patch(path, map[string]any{"notifyTo": []int64{}}), http.StatusOK, &all)
	if all.NotifyTo == nil || len(all.NotifyTo) != 0 {
		t.Errorf("all: %+v", all.NotifyTo)
	}
}

func TestChangeSolverAndKeyType(t *testing.T) {
	f := newFixture(t)
	c := f.issueNew("*.example.com")
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)
	other := f.provider("fake.records")
	var got CertificateBody
	f.call(f.api.Patch(path, map[string]any{"solverProvider": other, "keyType": "ecdsa-p384"}), http.StatusOK, &got)
	if got.SolverProvider != other || got.KeyType != "ecdsa-p384" {
		t.Fatalf("changed %+v", got)
	}
	for _, c := range []struct {
		body  map[string]any
		field string
		code  i18n.Key
	}{
		{map[string]any{"solverProvider": f.provider("fake.web")}, "body.solverProvider", i18n.CertsErrorSolverChallenge},
		{map[string]any{"solverProvider": f.provider("fake.hook")}, "body.solverProvider", ""},
	} {
		e := f.apiError(f.api.Patch(path, c.body), http.StatusUnprocessableEntity)
		if len(e.Fields) != 1 || e.Fields[0].Location != c.field || c.code != "" && e.Fields[0].Code != string(c.code) {
			t.Errorf("%v: %+v", c.body, e)
		}
	}
	// An uploaded one has no solver to change.
	up := f.add(f.leaf(30, "upload.example.com"))
	if e := f.apiError(f.api.Patch("/certificates/"+strconv.FormatInt(up.ID, 10), map[string]any{"keyType": "rsa-2048"}), http.StatusUnprocessableEntity); len(e.Fields) != 1 {
		t.Errorf("an uploaded one: %+v", e)
	}
}

func TestChangeSource(t *testing.T) {
	f := newFixture(t)
	ca, dns, files, spare := f.provider("fake.ca"), f.provider("fake.dns"), f.provider("fake.files"), f.provider("fake.spare")
	patch := func(c CertificateBody, body map[string]any) CertificateBody {
		t.Helper()
		var got CertificateBody
		f.call(f.api.Patch("/certificates/"+strconv.FormatInt(c.ID, 10), body), http.StatusOK, &got)
		return got
	}
	refused := func(c CertificateBody, body map[string]any, status int, field string, code i18n.Key) {
		t.Helper()
		e := f.apiError(f.api.Patch("/certificates/"+strconv.FormatInt(c.ID, 10), body), status)
		switch {
		case field == "" && e.Code != string(code):
			t.Errorf("%v: %+v", body, e)
		case field != "" && (len(e.Fields) != 1 || e.Fields[0].Location != field || code != "" && e.Fields[0].Code != string(code)):
			t.Errorf("%v: %+v", body, e)
		}
	}

	// An uploaded one, renewed from then on, with its key type.
	up := f.add(f.leaf(30, "upload.example.com"))
	refused(up, map[string]any{"sourceProvider": ca}, http.StatusUnprocessableEntity, "body.solverProvider", i18n.KernelErrorFieldRequired)
	refused(up, map[string]any{"sourceProvider": dns, "solverProvider": dns}, http.StatusUnprocessableEntity, "body.sourceProvider", "")
	got := patch(up, map[string]any{"sourceProvider": ca, "solverProvider": dns})
	if got.Source != sourceProvider || got.SourceProvider != ca || got.SolverProvider != dns || got.KeyType != up.KeyType || got.RenewAt == nil {
		t.Errorf("uploaded, then issued: %+v", got)
	}

	// Not while an issuance is under way.
	c := f.issueNew("example.com")
	refused(c, map[string]any{"sourceProvider": files}, http.StatusConflict, "", i18n.CertsErrorIssuingNow)
	f.run()
	// To a vendor, through files, with a backup; then to the files of
	// another client: no solver, no key type, no backup.
	vendor, web := f.provider("fake.vendor"), f.provider("fake.web")
	c = patch(c, map[string]any{"sourceProvider": vendor, "solverProvider": web, "backupProvider": spare})
	if c.SourceProvider != vendor || c.SolverProvider != web || c.BackupProvider != spare {
		t.Fatalf("to a vendor %+v", c)
	}
	got = patch(c, map[string]any{"sourceProvider": files})
	if got.SourceProvider != files || got.SolverProvider != 0 || got.KeyType != "" || got.BackupProvider != 0 {
		t.Errorf("to the files: %+v", got)
	}
	// And back to a CA, with a solver again and the key type of the
	// version in use.
	refused(got, map[string]any{"sourceProvider": ca}, http.StatusUnprocessableEntity, "body.solverProvider", i18n.KernelErrorFieldRequired)
	got = patch(got, map[string]any{"sourceProvider": ca, "solverProvider": dns})
	if got.SourceProvider != ca || got.SolverProvider != dns || got.Current == nil || got.KeyType != got.Current.KeyType {
		t.Errorf("back to a CA: %+v", got)
	}
	// One to buy, waiting with its CSR and no version yet, is issued by its
	// new source at once.
	var bought CertificateBody
	f.call(f.api.Post("/certificates/request", map[string]any{"domains": []string{"bought.example.com"}}), http.StatusCreated, &bought)
	if got = patch(bought, map[string]any{"sourceProvider": ca, "solverProvider": dns}); got.State != stateIssuing {
		t.Errorf("one to buy, now issued: %+v", got)
	}
	f.run()
	if got = f.certificate(bought.ID); got.Current == nil || got.State != stateOK {
		t.Errorf("one to buy, issued: %+v", got)
	}

	// The solver kept must do for the new source.
	byWeb := patch(f.add(f.leaf(30, "web.example.com")), map[string]any{"sourceProvider": vendor, "solverProvider": web})
	refused(byWeb, map[string]any{"sourceProvider": ca}, http.StatusUnprocessableEntity, "body.solverProvider", i18n.CertsErrorSolverChallenge)
}

func TestIssueBackup(t *testing.T) {
	f := newFixture(t)
	// The source takes files and records; its backup files only.
	source, records, web, spare := f.provider("fake.vendor"), f.provider("fake.records"), f.provider("fake.web"), f.provider("fake.spare")
	issue := func(backup, solver int64) map[string]any {
		return map[string]any{"domains": []string{"example.com"}, "sourceProvider": source, "solverProvider": solver, "backupProvider": backup, "keyType": "ecdsa-p256"}
	}
	for name, tt := range map[string]struct {
		body map[string]any
		code i18n.Key
	}{
		"not a backup":    {issue(f.provider("fake.acme"), web), i18n.CertsErrorBackupSource},
		"the source":      {issue(source, web), i18n.CertsErrorBackupSource},
		"another solver":  {issue(spare, records), i18n.CertsErrorBackupSolver},
		"not a source":    {issue(web, web), i18n.CertsErrorWrongProvider},
		"for its own key": {map[string]any{"domains": []string{"example.com"}, "sourceProvider": f.provider("fake.files"), "backupProvider": spare}, i18n.CertsErrorBackupSource},
	} {
		e := f.apiError(f.api.Post("/certificates/issue", tt.body), http.StatusUnprocessableEntity)
		if len(e.Fields) != 1 || e.Fields[0].Location != "body.backupProvider" || i18n.Key(e.Fields[0].Code) != tt.code {
			t.Errorf("%s: %+v", name, e.Fields)
		}
	}

	var c CertificateBody
	f.call(f.api.Post("/certificates/issue", issue(spare, web)), http.StatusCreated, &c)
	if c.BackupProvider != spare {
		t.Fatalf("backup %d, want %d", c.BackupProvider, spare)
	}
	// The source fails for good: its backup tries next all the same.
	f.failWith(retry.Permanent(errors.New("the CA is down")))
	f.run()
	is := f.certificate(c.ID).Issuance
	if is.State != "pending" || is.Attempts != 1 || f.fake.spared != 0 {
		t.Fatalf("after the source failed: %+v, %d from the backup", is, f.fake.spared)
	}
	f.now = *is.NextAt
	f.run()
	c = f.certificate(c.ID)
	if c.State != "ok" || c.Issuance.Attempts != 2 || f.fake.spared != 1 {
		t.Fatalf("after the backup: %s %+v, %d from the backup", c.State, c.Issuance, f.fake.spared)
	}
	d := f.issuanceDetail(c.Issuance.ID)
	if i := slices.IndexFunc(d.Log, func(l LogLineBody) bool { return l.Key == i18n.CertsLogBackupSource }); i < 0 || d.Log[i].Params["name"] != "fake.spare" {
		t.Errorf("log %v", logKeys(d))
	}
	got, err := f.svc.load(t.Context(), f.db, c.ID)
	if err != nil || got.Current.SourceProvider != spare {
		t.Fatalf("version from %d, want %d: %v", got.Current.SourceProvider, spare, err)
	}
	if leaf := replaced(got, spare); leaf == nil || replaced(got, source) != nil {
		t.Errorf("the renewal through the backup replaces %v, through the source %v", leaf, replaced(got, source))
	}

	// The next issuance starts with the source again.
	f.succeed()
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/issue"), http.StatusAccepted, nil)
	f.run()
	if got, err = f.svc.load(t.Context(), f.db, c.ID); err != nil || got.Current.SourceProvider != source || f.fake.spared != 1 {
		t.Errorf("renewed by %d, %d from the backup: %v", got.Current.SourceProvider, f.fake.spared, err)
	}

	// Without the backup, a failure for good is final.
	if err := f.reg.Delete(t.Context(), spare); err == nil {
		t.Error("deleted the backup in use")
	}
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)
	e := f.apiError(f.api.Patch(path, map[string]any{"solverProvider": records}), http.StatusUnprocessableEntity)
	if len(e.Fields) != 1 || e.Fields[0].Location != "body.solverProvider" || i18n.Key(e.Fields[0].Code) != i18n.CertsErrorBackupSolver {
		t.Errorf("a solver the backup cannot use: %+v", e.Fields)
	}
	c = CertificateBody{}
	f.call(f.api.Patch(path, map[string]any{"backupProvider": 0, "solverProvider": records}), http.StatusOK, &c)
	if c.BackupProvider != 0 || c.SolverProvider != records {
		t.Errorf("after removing the backup: %+v", c)
	}
	f.failWith(retry.Permanent(errors.New("the CA is down")))
	f.call(f.api.Post(path+"/issue"), http.StatusAccepted, nil)
	f.run()
	if is := f.certificate(c.ID).Issuance; is.State != "failed" || is.Attempts != 1 {
		t.Errorf("without a backup: %+v", is)
	}
	e = f.apiError(f.api.Patch(path, map[string]any{"backupProvider": spare}), http.StatusUnprocessableEntity)
	if len(e.Fields) != 1 || e.Fields[0].Location != "body.backupProvider" || i18n.Key(e.Fields[0].Code) != i18n.CertsErrorBackupSolver {
		t.Errorf("a backup that cannot use the solver: %+v", e.Fields)
	}
}

package acme

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	legoacme "github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/lego"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

// newModule returns the module on a fresh database, as Register leaves it.
func newModule(t *testing.T) *Module {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New()
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(t.Context(), "acme", m.Migrations()); err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	m.db, m.secrets, m.log = st.DB(), box.Scope("acme"), slog.New(slog.DiscardHandler)
	return m
}

func TestAccountType(t *testing.T) {
	typ := accountType()
	if typ.ID != "acme.account" || typ.Kind != providers.Source || len(typ.Fields) != 5 {
		t.Fatalf("type %+v", typ)
	}
	ca := typ.Fields[0]
	if ca.Key != "ca" || ca.Kind != providers.Select || ca.Default != lego.CodeLetsEncrypt {
		t.Errorf("ca field %+v", ca)
	}
	for _, o := range ca.Options {
		if o.Value == custom {
			continue
		}
		if _, err := lego.GetDirectoryURL(o.Value); err != nil {
			t.Errorf("option %s: %v", o.Value, err)
		}
	}
	if !typ.Fields[4].Secret || typ.Fields[3].Secret {
		t.Error("only the EAB HMAC key is secret")
	}
}

func TestDirectoryOf(t *testing.T) {
	in := func(config map[string]string) providers.Instance { return providers.Instance{Config: config} }
	if dir, err := directoryOf(in(map[string]string{"ca": lego.CodeLetsEncryptStaging})); err != nil || dir != lego.DirectoryURLLetsEncryptStaging {
		t.Errorf("staging: %s, %v", dir, err)
	}
	if dir, err := directoryOf(in(map[string]string{"ca": custom, "directory": "https://acme.example/dir"})); err != nil || dir != "https://acme.example/dir" {
		t.Errorf("custom: %s, %v", dir, err)
	}
	var p *contracts.Problem
	if _, err := directoryOf(in(map[string]string{"ca": custom})); !errors.As(err, &p) || p.Key != i18n.AcmeErrorDirectoryRequired {
		t.Errorf("custom without a directory: %v", err)
	}
	if _, err := directoryOf(in(map[string]string{"ca": "nope"})); err == nil {
		t.Error("an unknown CA has a directory")
	}
}

func TestEABHMAC(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"abc-_def", "abc-_def"},
		{" ab+c/d== ", "ab-c_d"}, // spaces around a pasted key
		{"YWJjZA", "YWJjZA"},
		{"YWJjZA==\n", "YWJjZA"},
		{"a+b/c+d/e+f==", "a-b_c-d_e-f"},
	} {
		if got := eabHMAC(tt.in); got != tt.want {
			t.Errorf("eabHMAC(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAccountKeys(t *testing.T) {
	m := newModule(t)
	ctx := t.Context()
	key, location, err := m.account(ctx, 1, lego.DirectoryURLLetsEncryptStaging)
	if err != nil || location != "" {
		t.Fatalf("a new account: %v, %q", err, location)
	}
	if err := m.registered(ctx, 1, "https://ca.example/acct/9"); err != nil {
		t.Fatal(err)
	}
	again, location, err := m.account(ctx, 1, lego.DirectoryURLLetsEncryptStaging)
	if err != nil || location != "https://ca.example/acct/9" || !sameKey(key, again) {
		t.Errorf("the same account: %v, %q", err, location)
	}
	// Another CA means another account, with a key of its own.
	other, location, err := m.account(ctx, 1, lego.DirectoryURLZeroSSL)
	if err != nil || location != "" || sameKey(key, other) {
		t.Errorf("after a change of CA: %v, %q", err, location)
	}
	// Each instance has its own.
	second, _, err := m.account(ctx, 2, lego.DirectoryURLZeroSSL)
	if err != nil || sameKey(other, second) {
		t.Errorf("a second instance shares a key: %v", err)
	}
}

func sameKey(a, b crypto.Signer) bool {
	return a.Public().(interface{ Equal(crypto.PublicKey) bool }).Equal(b.Public())
}

// fakeSolver records what it is asked to do.
type fakeSolver struct {
	mu    sync.Mutex
	calls []string
}

func (s *fakeSolver) Present(_ context.Context, ch contracts.Challenge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "present "+ch.Domain)
	return nil
}

func (s *fakeSolver) CleanUp(_ context.Context, ch contracts.Challenge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "cleanup "+ch.Domain)
	return nil
}

func (*fakeSolver) Wait() (time.Duration, time.Duration) { return 2 * time.Minute, 5 * time.Second }
func (*fakeSolver) Close() error                         { return nil }

func TestDNSProvider(t *testing.T) {
	s := &fakeSolver{}
	var logged []i18n.Key
	p := &dnsProvider{solver: s, log: func(l contracts.LogLine) { logged = append(logged, l.Key) }}
	if err := p.Present(t.Context(), "example.com", "t", "k"); err != nil {
		t.Fatal(err)
	}
	if err := p.CleanUp(t.Context(), "example.com", "t", "k"); err != nil {
		t.Fatal(err)
	}
	if timeout, interval := p.Timeout(); timeout != 2*time.Minute || interval != 5*time.Second {
		t.Errorf("Timeout = %v, %v", timeout, interval)
	}
	p.Timeout() // logs the wait once
	if !slices.Equal(s.calls, []string{"present example.com", "cleanup example.com"}) ||
		!slices.Equal(logged, []i18n.Key{i18n.AcmeLogPresent, i18n.AcmeLogCleanup, i18n.AcmeLogWait}) {
		t.Errorf("calls %v, log %v", s.calls, logged)
	}
}

func TestExplain(t *testing.T) {
	problem := func(typ string) error {
		return fmt.Errorf("acme: obtain: %w", &legoacme.ProblemDetails{Type: typ, Detail: "no", HTTPStatus: 403})
	}
	fromSolver := &contracts.Problem{Key: i18n.AcmeErrorPresent, Params: map[string]any{"domain": "example.com"}}
	for name, tt := range map[string]struct {
		err       error
		key       i18n.Key
		permanent bool
		delay     time.Duration
	}{
		"solver":       {fmt.Errorf("solve: %w", fromSolver), i18n.AcmeErrorPresent, false, 0},
		"rate limited": {&legoacme.RateLimitedError{ProblemDetails: &legoacme.ProblemDetails{Type: legoacme.RateLimitedErrorType}, RetryAfter: time.Hour}, i18n.AcmeErrorRateLimited, false, time.Hour},
		"eab":          {problem(legoacme.ExternalAccountRequiredErrorType), i18n.AcmeErrorEabRequired, true, 0},
		"rejected":     {problem(legoacme.RejectedIdentifierErrorType), i18n.AcmeErrorRejected, true, 0},
		"user action":  {problem(legoacme.UserActionRequiredErrorType), i18n.AcmeErrorUserAction, true, 0},
		"validation":   {problem(legoacme.UnauthorizedErrorType), i18n.AcmeErrorValidation, false, 0},
		"server":       {problem(legoacme.ServerInternalErrorType), i18n.AcmeErrorCa, false, 0},
		"compound": {&legoacme.ProblemDetails{Type: legoacme.CompoundErrorType, SubProblems: []legoacme.SubProblem{
			{Type: legoacme.CaaErrorType, Identifier: legoacme.Identifier{Value: "example.com"}},
		}}, i18n.AcmeErrorCaa, false, 0},
		"propagation": {errors.New("dns01: time limit exceeded: last error: NXDOMAIN"), i18n.AcmeErrorPropagation, false, 0},
	} {
		err := explain(tt.err, []string{"example.com"})
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != tt.key {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if retry.IsPermanent(err) != tt.permanent {
			t.Errorf("%s: permanent %v", name, retry.IsPermanent(err))
		}
		if d, _ := retry.Delay(err); d != tt.delay {
			t.Errorf("%s: delay %v", name, d)
		}
	}
	if err := explain(errors.New("connection reset"), nil); err.Error() != "connection reset" {
		t.Errorf("an unknown error became %v", err)
	}
}

func TestPrecheck(t *testing.T) {
	var logged []contracts.LogLine
	p := &dnsProvider{solver: &fakeSolver{}, log: func(l contracts.LogLine) { logged = append(logged, l) }, missed: map[string]bool{}}
	visible := false
	check := func(context.Context, string, string) (bool, error) {
		if visible {
			return true, nil
		}
		return false, errors.New("NXDOMAIN")
	}
	for range 3 {
		if ok, err := p.precheck(t.Context(), "a.example.com", "_acme-challenge.a.example.com.", "v", check); ok || err == nil {
			t.Fatalf("a missing record: %v, %v", ok, err)
		}
	}
	if len(logged) != 1 || logged[0].Key != i18n.AcmeLogNotVisible || logged[0].Detail != "NXDOMAIN" || logged[0].Step != contracts.StepValidate {
		t.Errorf("logged %+v", logged)
	}
	if got := p.unseen(); !slices.Equal(got, []string{"a.example.com"}) {
		t.Errorf("unseen %v", got)
	}
	visible = true
	if ok, err := p.precheck(t.Context(), "a.example.com", "_acme-challenge.a.example.com.", "v", check); !ok || err != nil {
		t.Errorf("a visible record: %v, %v", ok, err)
	}
	if got := p.unseen(); len(got) != 0 {
		t.Errorf("unseen once visible: %v", got)
	}

	// A stop ends the wait at once, with the reason.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if ok, err := p.precheck(ctx, "b.example.com", "_acme-challenge.b.example.com.", "v", check); !ok || !errors.Is(err, context.Canceled) {
		t.Errorf("after the stop: %v, %v", ok, err)
	}
	p.skipCheck = true
	visible = false
	if ok, err := p.precheck(t.Context(), "c.example.com", "_acme-challenge.c.example.com.", "v", check); !ok || err != nil {
		t.Errorf("skipping the check: %v, %v", ok, err)
	}
}

func TestTestUnreachable(t *testing.T) {
	m := newModule(t)
	src := &source{m: m, in: providers.Instance{ID: 1, Config: map[string]string{"ca": custom, "directory": "https://127.0.0.1:9/directory"}}}
	_, err := src.Test(t.Context())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.AcmeErrorDirectory {
		t.Errorf("a CA that is not there: %v", err)
	}
}

package acme

import (
	"context"
	"crypto"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-acme/lego/v5/lego"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
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
	if !slices.Equal(s.calls, []string{"present example.com", "cleanup example.com"}) ||
		!slices.Equal(logged, []i18n.Key{i18n.AcmeLogPresent, i18n.AcmeLogCleanup}) {
		t.Errorf("calls %v, log %v", s.calls, logged)
	}
}

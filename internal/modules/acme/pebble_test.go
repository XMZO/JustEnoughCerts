package acme

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// TestPebble issues certificates from Pebble, the test CA of Let's
// Encrypt. It runs when JEC_TEST_PEBBLE names Pebble's directory URL and
// JEC_TEST_PEBBLE_ROOT the PEM file of its root (test/certs/pebble.minica.pem
// in its image), as CI sets them. Pebble must run with
// PEBBLE_VA_ALWAYS_VALID=1, as no DNS record really appears, and
// PEBBLE_AUTHZREUSE=0, so that the second order is validated again rather
// than reusing the first one's authorizations, which Pebble does half the
// time by default.
func TestPebble(t *testing.T) {
	directory, rootFile := os.Getenv("JEC_TEST_PEBBLE"), os.Getenv("JEC_TEST_PEBBLE_ROOT")
	if directory == "" || rootFile == "" {
		t.Skip("JEC_TEST_PEBBLE and JEC_TEST_PEBBLE_ROOT are not set")
	}
	root, err := os.ReadFile(rootFile)
	if err != nil {
		t.Fatal(err)
	}
	m := newModule(t)
	m.roots = x509.NewCertPool()
	if !m.roots.AppendCertsFromPEM(root) {
		t.Fatal("no certificate in", rootFile)
	}
	m.skipPropagation = true
	src := &source{m: m, in: providers.Instance{ID: 1, Config: map[string]string{"ca": custom, "directory": directory, "email": "admin@example.com"}}}

	// A test registers the account, as the first issuance would, and tells
	// the profiles Pebble offers.
	msg, err := src.Test(t.Context())
	if err != nil || msg.Key != i18n.AcmeTestOk && msg.Key != i18n.AcmeTestProfiles {
		t.Fatalf("test: %+v, %v", msg, err)
	}
	t.Logf("test: %+v", msg)

	// The second order renews the first certificate, which Pebble takes;
	// the third says it renews one Pebble never issued, which must not keep
	// it from issuing.
	ca := cryptoxtest.NewCA(t)
	foreign := ca.Leaf(t, cryptoxtest.NewKey(t), time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "jec.example.com")
	var first *x509.Certificate
	for round := range 3 {
		key := cryptoxtest.NewKey(t)
		solver := &fakeSolver{}
		var steps []i18n.Key
		req := contracts.IssueRequest{
			Domains: []string{"jec.example.com", "*.jec.example.com"}, Key: key, Solver: solver,
			Log: func(l contracts.LogLine) { steps = append(steps, l.Key) },
		}
		switch round {
		case 1:
			req.Replaces = first
		case 2:
			req.Replaces = foreign
		}
		chain, err := src.Issue(t.Context(), req)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		leaf := chain[0]
		switch {
		case round == 0:
			first = leaf
			info, err := src.RenewalInfo(t.Context(), leaf)
			if err != nil || !info.End.After(info.Start) || !info.End.Before(leaf.NotAfter) {
				t.Errorf("renewal info: %+v %v", info, err)
			}
		case round == 1 && slices.Contains(steps, i18n.AcmeLogReplacesRefused):
			t.Errorf("Pebble refused the renewal of its own certificate: %v", steps)
		case round == 2:
			t.Logf("replacing a foreign certificate: refused and ordered again: %v", slices.Contains(steps, i18n.AcmeLogReplacesRefused))
		}
		if !slices.Contains(leaf.DNSNames, "jec.example.com") || !slices.Contains(leaf.DNSNames, "*.jec.example.com") ||
			!key.PublicKey.Equal(leaf.PublicKey) || len(chain) < 2 {
			t.Errorf("round %d: leaf for %v, chain of %d", round, leaf.DNSNames, len(chain))
		}
		if !slices.Contains(solver.calls, "present jec.example.com") || !slices.Contains(solver.calls, "cleanup jec.example.com") {
			t.Errorf("round %d: solver calls %v (does Pebble run with PEBBLE_AUTHZREUSE=0?)", round, solver.calls)
		}
		// The account was registered by the test.
		if slices.Contains(steps, i18n.AcmeLogRegister) || steps[len(steps)-1] != i18n.AcmeLogIssued {
			t.Errorf("round %d: steps %v", round, steps)
		}
	}

	// A profile the CA does not offer fails the test; one it offers, as
	// Pebble's shortlived, orders with it.
	odd := &source{m: m, in: providers.Instance{ID: 1, Config: map[string]string{"ca": custom, "directory": directory, "profile": "no-such-profile"}}}
	var p *contracts.Problem
	if _, err := odd.Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.AcmeErrorProfile {
		t.Errorf("an unknown profile: %v", err)
	}
	if msg.Key == i18n.AcmeTestProfiles && strings.Contains(fmt.Sprint(msg.Params["profiles"]), "shortlived") {
		short := &source{m: m, in: providers.Instance{ID: 1, Config: map[string]string{"ca": custom, "directory": directory, "profile": "shortlived"}}}
		chain, err := short.Issue(t.Context(), contracts.IssueRequest{Domains: []string{"short.jec.example.com"}, Key: cryptoxtest.NewKey(t), Solver: &fakeSolver{}, Log: func(contracts.LogLine) {}})
		// Pebble's shortlived certificates are valid for six days.
		if err != nil || chain[0].NotAfter.Sub(chain[0].NotBefore) > 7*24*time.Hour {
			t.Errorf("shortlived: %v", err)
		}
		// An address, proved by a file on the web server.
		files := &fakeFiles{}
		chain, err = short.Issue(t.Context(), contracts.IssueRequest{Domains: []string{"192.0.2.10"}, Key: cryptoxtest.NewKey(t), Files: files, Log: func(contracts.LogLine) {}})
		if err != nil || len(chain[0].IPAddresses) != 1 || chain[0].IPAddresses[0].String() != "192.0.2.10" || len(files.put) == 0 {
			t.Errorf("an address: %v, files %v", err, files.put)
		}
	}

	// A preferred chain: the one whose top is issued by the root
	// JEC_TEST_PEBBLE_PREFER names, an alternate root Pebble offers with
	// PEBBLE_ALTERNATE_ROOTS; one that Pebble does not offer leaves the
	// default chain.
	for _, name := range []string{os.Getenv("JEC_TEST_PEBBLE_PREFER"), "No Such Root"} {
		if name == "" {
			continue
		}
		pref := &source{m: m, in: providers.Instance{ID: 1, Config: map[string]string{"ca": custom, "directory": directory, "preferred_chain": name}}}
		chain, err := pref.Issue(t.Context(), contracts.IssueRequest{Domains: []string{"chain.jec.example.com"}, Key: cryptoxtest.NewKey(t), Solver: &fakeSolver{}, Log: func(contracts.LogLine) {}})
		if err != nil || len(chain) < 2 {
			t.Fatalf("preferring %q: %v", name, err)
		}
		top := chain[len(chain)-1].Issuer.CommonName
		if name != "No Such Root" && top != name {
			t.Errorf("preferring %q: a chain up to %q", name, top)
		}
		t.Logf("preferring %q: a chain up to %q", name, top)
	}

	// The first certificate, replaced, is revoked; asking again is no
	// error, as Pebble says it has been revoked already.
	for range 2 {
		if err := src.Revoke(t.Context(), first, contracts.ReasonSuperseded); err != nil {
			t.Errorf("revoke: %v", err)
		}
	}
}

// fakeFiles keeps the paths it is asked to put.
type fakeFiles struct{ put []string }

func (f *fakeFiles) PutFile(_ context.Context, file contracts.File) error {
	f.put = append(f.put, file.Domain+file.Path)
	return nil
}
func (*fakeFiles) RemoveFile(context.Context, contracts.File) error { return nil }
func (*fakeFiles) Close() error                                     { return nil }

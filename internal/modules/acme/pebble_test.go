package acme

import (
	"crypto/x509"
	"os"
	"slices"
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

	// A test registers the account, as the first issuance would.
	if msg, err := src.Test(t.Context()); err != nil || msg.Key != i18n.AcmeTestOk {
		t.Fatalf("test: %+v, %v", msg, err)
	}

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

	// The first certificate, replaced, is revoked; asking again is no
	// error, as Pebble says it has been revoked already.
	for range 2 {
		if err := src.Revoke(t.Context(), first, contracts.ReasonSuperseded); err != nil {
			t.Errorf("revoke: %v", err)
		}
	}
}

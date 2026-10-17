package acme

import (
	"crypto/x509"
	"os"
	"slices"
	"testing"

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

	for round := range 2 {
		key := cryptoxtest.NewKey(t)
		solver := &fakeSolver{}
		var steps []i18n.Key
		chain, err := src.Issue(t.Context(), contracts.IssueRequest{
			Domains: []string{"jec.example.com", "*.jec.example.com"}, Key: key, Solver: solver,
			Log: func(l contracts.LogLine) { steps = append(steps, l.Key) },
		})
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		leaf := chain[0]
		if !slices.Contains(leaf.DNSNames, "jec.example.com") || !slices.Contains(leaf.DNSNames, "*.jec.example.com") ||
			!key.PublicKey.Equal(leaf.PublicKey) || len(chain) < 2 {
			t.Errorf("round %d: leaf for %v, chain of %d", round, leaf.DNSNames, len(chain))
		}
		if !slices.Contains(solver.calls, "present jec.example.com") || !slices.Contains(solver.calls, "cleanup jec.example.com") {
			t.Errorf("round %d: solver calls %v (does Pebble run with PEBBLE_AUTHZREUSE=0?)", round, solver.calls)
		}
		// The account is registered once.
		registered := slices.Contains(steps, i18n.AcmeLogRegister)
		if registered != (round == 0) || steps[len(steps)-1] != i18n.AcmeLogIssued {
			t.Errorf("round %d: steps %v", round, steps)
		}
	}
}

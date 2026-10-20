package providersapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

// fakeSolver keeps the records it is asked for, failing to add them when
// its instance is set to.
type fakeSolver struct {
	mu      *sync.Mutex
	records map[string]string
	fail    bool
	cleaned *[]string
}

func (s *fakeSolver) Present(_ context.Context, ch contracts.Challenge) error {
	if s.fail {
		return &contracts.Problem{Key: i18n.KernelErrorProviderTest}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records["_acme-challenge."+ch.Domain] = ch.KeyAuth
	return nil
}

func (s *fakeSolver) CleanUp(_ context.Context, ch contracts.Challenge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, "_acme-challenge."+ch.Domain)
	*s.cleaned = append(*s.cleaned, ch.Domain)
	return nil
}

func (s *fakeSolver) Wait() (time.Duration, time.Duration) { return time.Minute, time.Second }
func (s *fakeSolver) Close() error                         { return nil }

func TestTry(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := providers.New(st.DB(), box.Scope("kernel"), func(string) bool { return true })
	var mu sync.Mutex
	records := map[string]string{}
	var cleaned []string
	scope := reg.Scope("fake")
	for _, typ := range []providers.Type{
		{ID: "fake.dns", Kind: providers.Solver, Brand: "DNS", Fields: []providers.Field{{Key: "FAIL", Kind: providers.Switch}}},
		{ID: "fake.manual", Kind: providers.Solver, Brand: "Manual", Manual: true},
		{ID: "fake.web", Kind: providers.Solver, Brand: "Web", Challenge: providers.ChallengeHTTP},
	} {
		if err := scope.Add(typ, func(_ context.Context, in providers.Instance) (any, error) {
			return &fakeSolver{mu: &mu, records: records, fail: in.Config["FAIL"] == "true", cleaned: &cleaned}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	reg.Seal()
	_, a := humatest.New(t)
	Register(a, reg)
	// The DNS answers what the solver added, but for late.example.com.
	// _acme-challenge.delegated.example.com is a CNAME to
	// delegated.acme.example.net, where the provider puts the record.
	orig, origDelegated := visible, delegated
	t.Cleanup(func() { visible, delegated = orig, origDelegated })
	visible = func(_ context.Context, name, typ, _ string) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		switch name {
		case "_acme-challenge.late.example.com", "_acme-challenge.delegated.example.com":
			return false, nil
		case "delegated.acme.example.net":
			name = "_acme-challenge.delegated.example.com"
		}
		return typ == "TXT" && records[name] != "", nil
	}
	delegated = func(_ context.Context, domain string) string {
		if domain == "delegated.example.com" {
			return "delegated.acme.example.net"
		}
		return ""
	}
	create := func(typ string, config map[string]string) string {
		id, err := reg.Create(t.Context(), typ, typ, config, nil)
		if err != nil {
			t.Fatal(err)
		}
		return strconv.FormatInt(id, 10)
	}
	dns, failing := create("fake.dns", nil), create("fake.dns", map[string]string{"FAIL": "true"})

	resp := a.Post("/providers/"+dns+"/try", map[string]any{"domain": "*.Example.COM."})
	var out TryBody
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &out) != nil {
		t.Fatalf("try = %d %s", resp.Code, resp.Body)
	}
	if out.Record != "_acme-challenge.example.com" || !out.Visible || !out.Removed || len(out.Value) != 43 {
		t.Errorf("tried %+v", out)
	}
	if len(records) != 0 || len(cleaned) != 1 {
		t.Errorf("left %v, cleaned %v", records, cleaned)
	}

	// A record delegated through a CNAME is looked for where it points.
	resp = a.Post("/providers/"+dns+"/try", map[string]any{"domain": "delegated.example.com"})
	out = TryBody{}
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &out) != nil || out.Via != "delegated.acme.example.net" || !out.Visible {
		t.Errorf("a delegated one = %d %s", resp.Code, resp.Body)
	}

	// One that refuses to add it answers with its message, and cleans up.
	resp = a.Post("/providers/"+failing+"/try", map[string]any{"domain": "example.com"})
	if resp.Code != http.StatusBadGateway {
		t.Errorf("a failing one = %d %s", resp.Code, resp.Body)
	}
	for _, c := range []struct {
		id, domain string
		status     int
	}{
		{dns, "nodot", http.StatusUnprocessableEntity},
		{create("fake.manual", nil), "example.com", http.StatusUnprocessableEntity},
		{create("fake.web", nil), "example.com", http.StatusUnprocessableEntity},
		{"999", "example.com", http.StatusNotFound},
	} {
		if resp := a.Post("/providers/"+c.id+"/try", map[string]any{"domain": c.domain}); resp.Code != c.status {
			t.Errorf("try %s on %s = %d, want %d", c.id, c.domain, resp.Code, c.status)
		}
	}
}

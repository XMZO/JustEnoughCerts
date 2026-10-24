package providersapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
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
// its instance is set to: with a message of its own, or, raw, with what
// a provider said.
type fakeSolver struct {
	mu      *sync.Mutex
	records map[string]string
	fail    bool
	raw     bool
	cleaned *[]string
}

func (s *fakeSolver) Present(_ context.Context, ch contracts.Challenge) error {
	if s.fail {
		return &contracts.Problem{Key: i18n.KernelErrorProviderTest}
	}
	if s.raw {
		return errors.New("zone dcv.cloudflare.com. not found")
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

// fakeFiles keeps the files it is asked for, by URL.
type fakeFiles struct {
	mu    *sync.Mutex
	files map[string]string
}

func (s *fakeFiles) PutFile(_ context.Context, f contracts.File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files["http://"+f.Domain+f.Path] = string(f.Content)
	return nil
}

func (s *fakeFiles) RemoveFile(_ context.Context, f contracts.File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, "http://"+f.Domain+f.Path)
	return nil
}

func (s *fakeFiles) Close() error { return nil }

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
	records, files := map[string]string{}, map[string]string{}
	var cleaned []string
	scope := reg.Scope("fake")
	for _, typ := range []providers.Type{
		{ID: "fake.dns", Kind: providers.Solver, Brand: "DNS", Fields: []providers.Field{{Key: "FAIL", Kind: providers.Switch}, {Key: "RAW", Kind: providers.Switch}}},
		{ID: "fake.manual", Kind: providers.Solver, Brand: "Manual", Manual: true},
		{ID: "fake.web", Kind: providers.Solver, Brand: "Web", Challenge: providers.ChallengeHTTP},
		{ID: "fake.files", Kind: providers.Solver, Brand: "Files", Challenge: providers.ChallengeHTTP},
	} {
		if err := scope.Add(typ, func(_ context.Context, in providers.Instance) (any, error) {
			if typ.ID == "fake.files" {
				return &fakeFiles{mu: &mu, files: files}, nil
			}
			return &fakeSolver{mu: &mu, records: records, fail: in.Config["FAIL"] == "true", raw: in.Config["RAW"] == "true", cleaned: &cleaned}, nil
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
		switch domain {
		case "delegated.example.com":
			return "delegated.acme.example.net"
		case "saas.example.com":
			return "saas.example.com.0123abcd.dcv.cloudflare.com"
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
	// What a provider said comes as it said it; first, when the record was
	// to go through a CNAME, where, and whose that name is.
	raw := create("fake.dns", map[string]string{"RAW": "true"})
	for domain, want := range map[string]string{
		"example.com":           `{"code":"kernel.error.provider_try","params":{"message":"zone dcv.cloudflare.com. not found"}}`,
		"saas.example.com":      `{"code":"kernel.error.provider_try_delegated","params":{"domain":"saas.example.com","kind":"cloudflare","message":"zone dcv.cloudflare.com. not found","via":"saas.example.com.0123abcd.dcv.cloudflare.com"}}`,
		"delegated.example.com": `{"code":"kernel.error.provider_try_delegated","params":{"domain":"delegated.example.com","kind":"other","message":"zone dcv.cloudflare.com. not found","via":"delegated.acme.example.net"}}`,
	} {
		resp = a.Post("/providers/"+raw+"/try", map[string]any{"domain": domain})
		if resp.Code != http.StatusBadGateway || strings.TrimSpace(resp.Body.String()) != want {
			t.Errorf("a raw failure on %s = %d %s", domain, resp.Code, resp.Body)
		}
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

	// A solver of files: the file is fetched from the domain, as a CA
	// does, and removed; on a domain served elsewhere, what it answered.
	origFetch, origFor := fetchFile, fetchFor
	t.Cleanup(func() { fetchFile, fetchFor = origFetch, origFor })
	fetchFor = 100 * time.Millisecond
	fetchFile = func(_ context.Context, url string) (int, []byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if body, ok := files[url]; ok && !strings.Contains(url, "elsewhere") {
			return http.StatusOK, []byte(body + "\n"), nil
		}
		return http.StatusNotFound, []byte("not found"), nil
	}
	web := create("fake.files", nil)
	out = TryBody{}
	resp = a.Post("/providers/"+web+"/try", map[string]any{"domain": "shop.example.com"})
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &out) != nil {
		t.Fatalf("try files = %d %s", resp.Code, resp.Body)
	}
	if out.Kind != "http" || !strings.HasPrefix(out.Record, "http://shop.example.com/.well-known/acme-challenge/jec-try-") || !out.Visible || !out.Removed || out.Answer != "" || len(files) != 0 {
		t.Errorf("tried files %+v, left %v", out, files)
	}
	out = TryBody{}
	resp = a.Post("/providers/"+web+"/try", map[string]any{"domain": "elsewhere.example.com"})
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &out) != nil || out.Visible || out.Answer != "HTTP 404" || !out.Removed {
		t.Errorf("a domain served elsewhere = %d %s", resp.Code, resp.Body)
	}
}

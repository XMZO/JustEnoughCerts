package lego

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/children"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnsrecord"
)

// The test binary runs the provider children too.
func TestMain(m *testing.M) {
	if children.IsChild() {
		os.Exit(children.Main(context.Background()))
	}
	os.Exit(m.Run())
}

func provider(t *testing.T, code string) catalogProvider {
	t.Helper()
	c, err := loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(c.Providers, func(p catalogProvider) bool { return p.Code == code })
	if i < 0 {
		t.Fatalf("no %s in the catalog", code)
	}
	return c.Providers[i]
}

func TestCatalog(t *testing.T) {
	c, err := loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Lego, "v5.") || len(c.Providers) < 200 {
		t.Errorf("lego %s with %d providers", c.Lego, len(c.Providers))
	}
	for _, p := range c.Providers {
		if slices.Contains(excluded, p.Code) {
			t.Errorf("the catalog offers %s", p.Code)
		}
	}
	typ := providerType(provider(t, "tencentcloud"))
	if typ.ID != "lego.tencentcloud" || typ.Kind != providers.Solver || typ.Brand != "Tencent Cloud DNS" || !slices.Equal(typ.Tags, []string{"china"}) {
		t.Errorf("tencentcloud: %+v", typ)
	}
	secret := map[string]bool{}
	for _, f := range typ.Fields {
		secret[f.Key] = f.Secret
	}
	if !secret["TENCENTCLOUD_SECRET_ID"] || !secret["TENCENTCLOUD_SECRET_KEY"] || secret["TENCENTCLOUD_REGION"] {
		t.Errorf("secret fields: %v", secret)
	}
}

func TestRecords(t *testing.T) {
	// JEC adds records of any type through some providers itself: their
	// solvers are RecordSolvers too, with the instance's settings.
	if !providerType(provider(t, "tencentcloud")).Records || providerType(provider(t, "httpreq")).Records {
		t.Error("records for the wrong providers")
	}
	if _, ok := open(t, "httpreq", httpreq("http://127.0.0.1:1")).(contracts.RecordSolver); ok {
		t.Error("httpreq adds records")
	}
	rs, ok := open(t, "tencentcloud", map[string]string{"TENCENTCLOUD_REGION": "ap-guangzhou"}).(contracts.RecordSolver)
	if !ok {
		t.Fatal("tencentcloud adds no records")
	}
	if _, err := rs.AddRecord(t.Context(), contracts.Record{Name: "_x.example.com", Type: "TXT", Value: "v"}); !errors.Is(err, dnsrecord.ErrUnsupported) {
		t.Errorf("without credentials: %v", err)
	}
}

// endpoint is an httpreq server: it records what it is asked to present
// and clean up.
type endpoint struct {
	*httptest.Server
	mu    sync.Mutex
	calls []string
	delay time.Duration
	fail  bool
}

func newEndpoint(t *testing.T) *endpoint {
	t.Helper()
	e := &endpoint{}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Domain, Token, KeyAuth string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.calls = append(e.calls, r.URL.Path+" "+body.Domain+" "+body.KeyAuth)
		delay, fail := e.delay, e.fail
		e.mu.Unlock()
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
		}
		if fail {
			http.Error(w, "the zone is locked", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(e.Close)
	return e
}

func (e *endpoint) got() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.calls)
}

func open(t *testing.T, code string, config map[string]string) contracts.Solver {
	t.Helper()
	p, err := factory(provider(t, code), slog.New(slog.DiscardHandler))(t.Context(), providers.Instance{ID: 7, Config: config, Secrets: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	s := p.(contracts.Solver)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func httpreq(url string) map[string]string {
	return map[string]string{
		"HTTPREQ_ENDPOINT": url, "HTTPREQ_MODE": "RAW",
		"HTTPREQ_PROPAGATION_TIMEOUT": "90", "HTTPREQ_POLLING_INTERVAL": "3",
	}
}

func TestSolver(t *testing.T) {
	e := newEndpoint(t)
	s := open(t, "httpreq", httpreq(e.URL))
	if timeout, interval := s.Wait(); timeout != 90*time.Second || interval != 3*time.Second {
		t.Errorf("Wait = %v, %v", timeout, interval)
	}
	ch := contracts.Challenge{Domain: "example.com", Token: "tok", KeyAuth: "tok.thumbprint"}
	if err := s.Present(t.Context(), ch); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanUp(t.Context(), ch); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{"/present example.com tok.thumbprint", "/cleanup example.com tok.thumbprint"}
	if got := e.got(); !slices.Equal(got, want) {
		t.Errorf("the endpoint got %q, want %q", got, want)
	}
}

func TestSolverErrors(t *testing.T) {
	e := newEndpoint(t)
	e.fail = true
	s := open(t, "httpreq", httpreq(e.URL))
	err := s.Present(t.Context(), contracts.Challenge{Domain: "example.com", Token: "t", KeyAuth: "k"})
	if err == nil || !strings.HasPrefix(err.Error(), "lego httpreq: ") || !strings.Contains(err.Error(), "500") {
		t.Errorf("a failing endpoint: %v", err)
	}
	// The child is still usable after an error it reported.
	e.mu.Lock()
	e.fail = false
	e.mu.Unlock()
	if err := s.CleanUp(t.Context(), contracts.Challenge{Domain: "example.com", Token: "t", KeyAuth: "k"}); err != nil {
		t.Errorf("after a reported error: %v", err)
	}

	missing := open(t, "httpreq", map[string]string{})
	err = missing.Present(t.Context(), contracts.Challenge{Domain: "example.com"})
	if err == nil || !strings.Contains(err.Error(), "HTTPREQ_ENDPOINT") {
		t.Errorf("without settings: %v", err)
	}
	if timeout, _ := missing.Wait(); timeout != defaultTimeout {
		t.Errorf("Wait of a provider that cannot start: %v", timeout)
	}
}

func TestSolverCancel(t *testing.T) {
	e := newEndpoint(t)
	e.delay = time.Minute
	s := open(t, "httpreq", httpreq(e.URL))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	start := time.Now()
	err := s.Present(ctx, contracts.Challenge{Domain: "example.com", Token: "t", KeyAuth: "k"})
	if err == nil || time.Since(start) > 20*time.Second {
		t.Fatalf("a call past its context: %v after %v", err, time.Since(start))
	}
	// A provider cut off mid-request is not trusted again.
	if err := s.CleanUp(t.Context(), contracts.Challenge{Domain: "example.com"}); err == nil {
		t.Error("a later call went to the killed child")
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close after a kill: %v", err)
	}
}

func TestLineWriter(t *testing.T) {
	var lines []string
	w := &lineWriter{log: func(l string) { lines = append(lines, l) }}
	_, _ = io.WriteString(w, "one\ntw")
	_, _ = io.WriteString(w, "o\n\n three ")
	w.flush()
	if !slices.Equal(lines, []string{"one", "two", "three"}) {
		t.Errorf("lines %q", lines)
	}
}

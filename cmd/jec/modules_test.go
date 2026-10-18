package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
)

// logBuffer keeps what JEC logs, for the setup token.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// session calls the API of a JEC with its session cookie.
type session struct {
	t      *testing.T
	srv    *httptest.Server
	cookie string
}

func (s *session) call(method, path, body string, out any) int {
	s.t.Helper()
	req, err := http.NewRequestWithContext(s.t.Context(), method, s.srv.URL+path, strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: s.cookie})
	}
	resp, err := s.srv.Client().Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			s.cookie = c.Value
		}
	}
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			s.t.Errorf("%s %s: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

// TestEverySwitchOff is the acceptance of M3 (PLAN.md 11): with any one
// module switched off while JEC runs, the rest of its API goes on working,
// and the provider types of that module are gone; switched on again, they
// are back. The UI is left out: a build without the web app has none.
func TestEverySwitchOff(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	var logs logBuffer
	app, err := kernel.New(t.Context(), cfg, slog.New(slog.NewTextHandler(&logs, nil)), modules)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	s := &session{t: t, srv: httptest.NewServer(app.Handler())}
	t.Cleanup(s.srv.Close)
	token := regexp.MustCompile(`setup_token=(\w+)`).FindStringSubmatch(logs.String())
	if token == nil {
		t.Fatal("no setup token in the log")
	}
	if status := s.call(http.MethodPost, "/api/v1/setup", `{"token": "`+token[1]+`", "username": "admin", "password": "correct horse"}`, nil); status != http.StatusCreated {
		t.Fatalf("setup: %d", status)
	}

	type manifest struct {
		Modules []struct {
			ID       string `json:"id"`
			Enabled  bool   `json:"enabled"`
			Required bool   `json:"required"`
		} `json:"modules"`
	}
	// types returns the IDs of the provider types of every kind.
	types := func() []string {
		var ids []string
		for _, kind := range []string{"source", "solver", "target", "notifier"} {
			var out struct {
				Types []struct {
					ID string `json:"id"`
				} `json:"types"`
			}
			if status := s.call(http.MethodGet, "/api/v1/providers/types?kind="+kind, "", &out); status != http.StatusOK {
				t.Errorf("%s types: %d", kind, status)
			}
			for _, typ := range out.Types {
				ids = append(ids, typ.ID)
			}
		}
		return ids
	}
	var first manifest
	if status := s.call(http.MethodGet, "/api/v1/manifest", "", &first); status != http.StatusOK || len(first.Modules) != len(modules) {
		t.Fatalf("manifest: %d, %d modules", status, len(first.Modules))
	}
	all := types()

	switched := 0
	for _, m := range first.Modules {
		if m.Required {
			continue
		}
		switched++
		if status := s.call(http.MethodPut, "/api/v1/modules/"+m.ID, `{"on": false}`, nil); status != http.StatusOK {
			t.Errorf("%s: switching off: %d", m.ID, status)
			continue
		}
		// The rest works, without the types of the module.
		var now manifest
		if status := s.call(http.MethodGet, "/api/v1/manifest", "", &now); status != http.StatusOK {
			t.Errorf("%s off: manifest %d", m.ID, status)
		}
		if i := slices.IndexFunc(now.Modules, func(n struct {
			ID       string `json:"id"`
			Enabled  bool   `json:"enabled"`
			Required bool   `json:"required"`
		}) bool {
			return n.ID == m.ID
		}); i < 0 || now.Modules[i].Enabled {
			t.Errorf("%s off: still on in the manifest", m.ID)
		}
		if slices.ContainsFunc(types(), func(id string) bool { return strings.HasPrefix(id, m.ID+".") }) {
			t.Errorf("%s off: its types are still there", m.ID)
		}
		for _, path := range []string{"/api/v1/session", "/api/v1/system", "/api/v1/providers"} {
			if status := s.call(http.MethodGet, path, "", nil); status != http.StatusOK {
				t.Errorf("%s off: %s %d", m.ID, path, status)
			}
		}
		if m.ID != "certs" {
			if status := s.call(http.MethodGet, "/api/v1/certs/certificates", "", nil); status != http.StatusOK {
				t.Errorf("%s off: certificates %d", m.ID, status)
			}
		}
		// On again, as it was.
		if status := s.call(http.MethodPut, "/api/v1/modules/"+m.ID, `{"on": true}`, nil); status != http.StatusOK {
			t.Errorf("%s: switching on: %d", m.ID, status)
		}
		if !m.Enabled {
			s.call(http.MethodPut, "/api/v1/modules/"+m.ID, `{"on": false}`, nil)
		}
	}
	if switched < len(modules)/2 {
		t.Errorf("switched %d of %d modules", switched, len(modules))
	}
	if got := types(); !slices.Equal(got, all) {
		t.Errorf("types after switching every module on again:\n%v\nwant\n%v", got, all)
	}
}

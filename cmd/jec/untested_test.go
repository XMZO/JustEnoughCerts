package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
)

// TestUntestedTags keeps the "Untested" tag of the provider types and the
// table of docs/reference/providers.md in step: a type tested only against
// a fake is tagged, one tried against a real service, real software or an
// emulator is not, and every type is in the table.
func TestUntestedTags(t *testing.T) {
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
	// The experimental modules too.
	var manifest struct {
		Modules []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"modules"`
	}
	s.call(http.MethodGet, "/api/v1/manifest", "", &manifest)
	for _, m := range manifest.Modules {
		if !m.Enabled {
			s.call(http.MethodPut, "/api/v1/modules/"+m.ID, `{"on": true}`, nil)
		}
	}
	untested := map[string]bool{}
	for _, kind := range []string{"source", "solver", "target", "notifier"} {
		var out struct {
			Types []struct {
				ID       string `json:"id"`
				Untested bool   `json:"untested"`
			} `json:"types"`
		}
		if status := s.call(http.MethodGet, "/api/v1/providers/types?kind="+kind, "", &out); status != http.StatusOK {
			t.Fatalf("%s types: %d", kind, status)
		}
		for _, typ := range out.Types {
			untested[typ.ID] = typ.Untested
		}
	}

	doc, err := os.ReadFile("../../docs/reference/providers.md")
	if err != nil {
		t.Fatal(err)
	}
	// A row: the types, their description, and what they were tried with.
	row := regexp.MustCompile("(?m)^\\| (`[^|]*`) \\| [^|]* \\| ([^|]*) \\|$")
	id := regexp.MustCompile("`([^`]+)`")
	documented := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(doc), -1) {
		for _, i := range id.FindAllStringSubmatch(m[1], -1) {
			documented[i[1]] = strings.TrimSpace(m[2])
		}
	}
	covered := func(typ string) (string, bool) {
		if how, ok := documented[typ]; ok {
			return how, true
		}
		for pattern, how := range documented {
			if prefix, ok := strings.CutSuffix(pattern, "*"); ok && strings.HasPrefix(typ, prefix) {
				return how, true
			}
		}
		return "", false
	}
	var ids []string
	for typ := range untested {
		ids = append(ids, typ)
	}
	slices.Sort(ids)
	for _, typ := range ids {
		how, ok := covered(typ)
		if !ok {
			t.Errorf("%s is not in docs/reference/providers.md", typ)
			continue
		}
		tried := strings.Contains(how, "Real") || strings.Contains(how, "Emulator")
		fakeOnly := !tried && (how == "Fake" || strings.HasPrefix(how, "Fake,") || strings.HasPrefix(how, "Fake;") || strings.HasPrefix(how, "Fake API"))
		switch {
		case tried && untested[typ]:
			t.Errorf("%s is tagged Untested, but was tried: %s", typ, how)
		case fakeOnly && !untested[typ] && !strings.HasPrefix(typ, "lego."):
			t.Errorf("%s was tried with a fake only (%s), but is not tagged Untested", typ, how)
		}
	}
}

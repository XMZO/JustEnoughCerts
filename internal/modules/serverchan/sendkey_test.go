package serverchan

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

const (
	turboKey = "SCT123456TaBcDeFgHiJ"
	sc3Key   = "sctp7890tKlMnOpQrSt"
)

// serve starts a stand-in for both APIs of ServerChan: Turbo's under
// /turbo/, and ServerChan3's under /sc3/<uid>/.
func serve(t *testing.T, h http.HandlerFunc) *Module {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	m := New()
	m.client, m.turbo, m.sc3 = srv.Client(), srv.URL+"/turbo/", srv.URL+"/sc3/%s/"
	return m
}

func open(t *testing.T, m *Module, key string) *sendKey {
	t.Helper()
	p, err := m.factory(t.Context(), providers.Instance{Secrets: map[string]string{"key": " " + key + " "}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*sendKey)
}

var note = contracts.Note{
	Event: contracts.CertificateIssueFailed, Title: "example.com failed", Subject: "[JEC] example.com failed",
	Text: "The DNS record did not show up.\nDetails: https://jec.example.com/certificates/3",
}

func TestNotify(t *testing.T) {
	var path string
	var got map[string]any
	m := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		path, got = r.URL.Path, nil
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"code":0,"message":"","data":{"pushid":"1","readkey":"x","error":"SUCCESS","errno":0}}`)
	})

	// Turbo, which is told not to show the address of JEC.
	k := open(t, m, turboKey)
	if err := k.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	if path != "/turbo/"+turboKey+".send" || got["title"] != note.Subject || got["short"] != "The DNS record did not show up." ||
		got["desp"] != "The DNS record did not show up.\n\nDetails: https://jec.example.com/certificates/3" || got["noip"] != 1.0 || got["tags"] != nil {
		t.Errorf("sent %s %v", path, got)
	}
	if err := k.Close(); err != nil {
		t.Error(err)
	}

	// ServerChan3, at the address of the uid in the key.
	if err := open(t, m, sc3Key).Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	if path != "/sc3/7890/"+sc3Key+".send" || got["title"] != note.Subject || got["tags"] != "JEC" || got["noip"] != nil {
		t.Errorf("sent %s %v", path, got)
	}
}

func TestLongTitle(t *testing.T) {
	var got struct{ Title, Desp string }
	m := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"code":0,"message":""}`)
	})
	title := "[JEC] " + strings.Repeat("ab"+string(rune(0x4e2d)), 20)
	if err := open(t, m, turboKey).Notify(t.Context(), contracts.Note{Subject: title, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	// The card takes 32 characters; the page has the whole title.
	if n := utf8.RuneCountInString(got.Title); n != maxTitle || !strings.HasSuffix(got.Title, ellipsis) || got.Desp != escape(title)+"\n\nx" {
		t.Errorf("sent %v", got)
	}
}

func TestMarkdown(t *testing.T) {
	bs := string(rune(92))
	for in, want := range map[string]string{
		"plain text, 2026-10-18.":                              "plain text, 2026-10-18.",
		"*.example.com and _acme-challenge [x] <b> a|b ~c~ #1": bs + "*.example.com and " + bs + "_acme-challenge " + bs + "[x" + bs + "] " + bs + "<b" + bs + "> a" + bs + "|b " + bs + "~c" + bs + "~ " + bs + "#1",
		"see https://example.com/a_b*c, then _x_":              "see https://example.com/a_b*c, then " + bs + "_x" + bs + "_",
		"a " + bs + " b `c`":                                   "a " + bs + bs + " b " + bs + "`c" + bs + "`",
	} {
		if got := markdown(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestNotifyErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{200, `{"code":40001,"message":"bad pushtoken","data":null}`, i18n.ServerchanErrorKey, true},
		{200, `{"code":40001,"message":"sendkey error","data":null}`, i18n.ServerchanErrorKey, true},
		{200, `{"code":20001,"message":"daily limit reached","data":null}`, i18n.ServerchanErrorApi, true},
		{500, `{"code":50000,"message":"server error"}`, i18n.ServerchanErrorApi, false},
		{429, `Too Many Requests`, i18n.ServerchanErrorThrottled, false},
		{502, `<html>bad gateway</html>`, i18n.ServerchanErrorStatus, false},
		{404, `{}`, i18n.ServerchanErrorStatus, true},
	} {
		m := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		})
		err := open(t, m, turboKey).Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
}

func TestBadKey(t *testing.T) {
	m := serve(t, func(http.ResponseWriter, *http.Request) { t.Error("sent") })
	for _, key := range []string{"", "SCT1/../x", "https://sctapi.ftqq.com/SCT1.send"} {
		err := open(t, m, key).Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.ServerchanErrorKey || !retry.IsPermanent(err) {
			t.Errorf("%q: %v", key, err)
		}
	}
}

func TestNotifyNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	m := New()
	m.client, m.turbo = &http.Client{Timeout: 10 * time.Second}, srv.URL+"/"
	err := open(t, m, turboKey).Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.ServerchanErrorNetwork || retry.IsPermanent(err) {
		t.Fatalf("no network: %v", err)
	}
	if strings.Contains(err.Error(), turboKey) {
		t.Errorf("the error shows the key: %v", err)
	}
}

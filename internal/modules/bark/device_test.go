package bark

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

const key = "aBcDeF123456"

func open(t *testing.T, config map[string]string) *device {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"key": " " + key + " "}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*device)
}

// serve starts a stand-in for a Bark server and a device that reaches it.
func serve(t *testing.T, h http.HandlerFunc) *device {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return open(t, map[string]string{"server": srv.URL + "/bark/", "allow_private": "true"})
}

var note = contracts.Note{
	Event: contracts.CertificateIssueFailed, Title: "example.com failed", Subject: "[JEC] example.com failed",
	Text: "line 1\nline 2", Link: "https://jec.example.com/certificates/3",
}

func TestNotify(t *testing.T) {
	var got map[string]string
	d := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/bark/push" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		got = nil
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"code":200,"message":"success","timestamp":1700000000}`)
	})
	if err := d.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"device_key": key, "title": note.Subject, "body": note.Text, "group": "JEC", "url": note.Link}
	if len(got) != len(want) {
		t.Errorf("sent %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	// Without a page, tapping opens Bark.
	if err := d.Notify(t.Context(), contracts.Note{Title: "t", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["url"]; ok || got["title"] != "t" {
		t.Errorf("sent %v", got)
	}
	if err := d.Close(); err != nil {
		t.Error(err)
	}
}

func TestNotifyErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{400, `{"code":400,"message":"failed to get device token: failed to get [aBcDeF123456] device token from database","timestamp":1700000000}`, i18n.BarkErrorKey, true},
		{400, `{"code":400,"message":"device key is empty","timestamp":1700000000}`, i18n.BarkErrorKey, true},
		{500, `{"code":500,"message":"push failed: 410 Unregistered","timestamp":1700000000}`, i18n.BarkErrorApi, false},
		{400, `{"code":400,"message":"request bind failed","timestamp":1700000000}`, i18n.BarkErrorApi, true},
		{502, `<html>bad gateway</html>`, i18n.BarkErrorStatus, false},
		{404, `{"error":"not found"}`, i18n.BarkErrorStatus, true},
		{301, ``, i18n.BarkErrorRedirect, true},
	} {
		d := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			if c.status == 301 {
				w.Header().Set("Location", "https://bark.example.com/push")
			}
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		})
		err := d.Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
}

func TestServer(t *testing.T) {
	for _, server := range []string{"api.day.app", "ftp://api.day.app", "https://", "https://api.day.app/?x=1"} {
		_, err := factory(t.Context(), providers.Instance{Config: map[string]string{"server": server}})
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.BarkErrorServer || !retry.IsPermanent(err) {
			t.Errorf("%q: %v", server, err)
		}
	}
	// Bark's own server when none is set.
	if d := open(t, nil); d.server.String() != defaultServer {
		t.Errorf("server %s", d.server)
	}
}

func TestPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("reached") }))
	defer srv.Close()
	err := open(t, map[string]string{"server": srv.URL}).Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.BarkErrorPrivate || !retry.IsPermanent(err) {
		t.Errorf("a private address: %v", err)
	}
}

func TestNotifyNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	d := open(t, map[string]string{"server": srv.URL, "allow_private": "true"})
	d.client.Timeout = 10 * time.Second
	err := d.Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.BarkErrorNetwork || retry.IsPermanent(err) {
		t.Fatalf("no server: %v", err)
	}
}

package gotify

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

const token = "AbCdEf.123456"

func open(t *testing.T, config map[string]string) *app {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"token": " " + token + " "}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*app)
}

// serve starts a stand-in for a Gotify server and an app on it.
func serve(t *testing.T, h http.HandlerFunc) *app {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return open(t, map[string]string{"server": srv.URL + "/gotify/", "allow_private": "true"})
}

var note = contracts.Note{
	Event: contracts.CertificateIssueFailed, Title: "example.com failed", Subject: "[JEC] example.com failed",
	Text: "line 1\nline 2", Link: "https://jec.example.com/certificates/3",
}

// message is what an app pushes.
type message struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
	Extras   struct {
		Display struct {
			ContentType string `json:"contentType"`
		} `json:"client::display"`
		Notification *struct {
			Click struct {
				URL string `json:"url"`
			} `json:"click"`
		} `json:"client::notification"`
	} `json:"extras"`
}

func TestNotify(t *testing.T) {
	var got message
	a := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/gotify/message" || r.Header.Get("X-Gotify-Key") != token || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s %q", r.Method, r.URL.Path, r.Header.Get("X-Gotify-Key"))
		}
		got = message{}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"id":1,"appid":2,"message":"line 1","title":"example.com failed","priority":8,"date":"2026-10-18T08:00:00Z"}`)
	})
	if err := a.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	if got.Title != note.Title || got.Message != note.Text || got.Priority != priorityHigh || got.Extras.Display.ContentType != "text/plain" ||
		got.Extras.Notification == nil || got.Extras.Notification.Click.URL != note.Link {
		t.Errorf("sent %+v", got)
	}
	// Good news is not urgent; without a page, tapping opens Gotify.
	if err := a.Notify(t.Context(), contracts.Note{Event: contracts.CertificateDeployed, Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if got.Priority != priorityNormal || got.Message != "t" || got.Extras.Notification != nil {
		t.Errorf("sent %+v", got)
	}
	if err := a.Close(); err != nil {
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
		{401, `{"error":"Unauthorized","errorCode":401,"errorDescription":"you need to provide a valid access token or user credentials to access this api"}`, i18n.GotifyErrorToken, true},
		{403, `{"error":"Forbidden","errorCode":403,"errorDescription":"you are not allowed to access this api"}`, i18n.GotifyErrorToken, true},
		{400, `{"error":"Bad Request","errorCode":400,"errorDescription":"Field 'message' is required"}`, i18n.GotifyErrorApi, true},
		{500, `{"error":"Internal Server Error","errorCode":500,"errorDescription":"database is locked"}`, i18n.GotifyErrorApi, false},
		{502, `<html>bad gateway</html>`, i18n.GotifyErrorStatus, false},
		{404, `404 page not found`, i18n.GotifyErrorStatus, true},
		{302, ``, i18n.GotifyErrorRedirect, true},
	} {
		a := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			if c.status == 302 {
				w.Header().Set("Location", "https://evil.example.com/message")
			}
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		})
		err := a.Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
}

func TestServer(t *testing.T) {
	for _, server := range []string{"", "gotify.example.com", "ftp://gotify.example.com", "https://gotify.example.com/#x"} {
		_, err := factory(t.Context(), providers.Instance{Config: map[string]string{"server": server}})
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.GotifyErrorServer || !retry.IsPermanent(err) {
			t.Errorf("%q: %v", server, err)
		}
	}
}

func TestPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("reached") }))
	defer srv.Close()
	err := open(t, map[string]string{"server": srv.URL}).Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.GotifyErrorPrivate || !retry.IsPermanent(err) {
		t.Errorf("a private address: %v", err)
	}
}

func TestNotifyNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	err := open(t, map[string]string{"server": srv.URL, "allow_private": "true"}).Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.GotifyErrorNetwork || retry.IsPermanent(err) {
		t.Fatalf("no server: %v", err)
	}
}

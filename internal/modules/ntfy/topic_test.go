package ntfy

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

const name = "jec-alerts_7Qx"

func open(t *testing.T, config, secrets map[string]string) *topic {
	t.Helper()
	s := map[string]string{"topic": " " + name + " "}
	for k, v := range secrets {
		s[k] = v
	}
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: s})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*topic)
}

// serve starts a stand-in for an ntfy server and a topic on it.
func serve(t *testing.T, secrets map[string]string, h http.HandlerFunc) *topic {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return open(t, map[string]string{"server": srv.URL + "/ntfy/", "allow_private": "true"}, secrets)
}

var note = contracts.Note{
	Event: contracts.CertificateIssueFailed, Title: "example.com failed", Subject: "[JEC] example.com failed",
	Text: "line 1\nline 2", Link: "https://jec.example.com/certificates/3",
}

// message is what a topic publishes.
type message struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags"`
	Click    string   `json:"click"`
}

func TestNotify(t *testing.T) {
	var auth string
	var got message
	tp := serve(t, map[string]string{"token": "tk_s3cret"}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ntfy/" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		auth, got = r.Header.Get("Authorization"), message{}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"id":"abc","time":1700000000,"event":"message","topic":"`+name+`"}`)
	})
	if err := tp.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tk_s3cret" || got.Topic != name || got.Title != note.Subject || got.Message != note.Text ||
		got.Priority != 4 || len(got.Tags) != 1 || got.Tags[0] != "warning" || got.Click != note.Link {
		t.Errorf("sent %q %+v", auth, got)
	}
	// A success has the default priority and a check mark; a test, neither.
	if err := tp.Notify(t.Context(), contracts.Note{Event: contracts.CertificateIssued, Title: "t", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if got.Priority != 3 || len(got.Tags) != 1 || got.Tags[0] != "white_check_mark" || got.Click != "" {
		t.Errorf("sent %+v", got)
	}
	if err := tp.Notify(t.Context(), contracts.Note{Event: contracts.NoteTest, Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if got.Priority != 0 || got.Tags != nil || got.Message != "t" {
		t.Errorf("sent %+v", got)
	}
	if err := tp.Close(); err != nil {
		t.Error(err)
	}
}

func TestNoToken(t *testing.T) {
	tp := serve(t, nil, func(_ http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Authorization"); a != "" {
			t.Errorf("authorization %q", a)
		}
	})
	if err := tp.Notify(t.Context(), note); err != nil {
		t.Error(err)
	}
}

func TestLongMessage(t *testing.T) {
	var got message
	tp := serve(t, nil, func(_ http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
	})
	if err := tp.Notify(t.Context(), contracts.Note{Title: "t", Text: strings.Repeat("ab"+string(rune(0x4e2d)), 1000)}); err != nil {
		t.Fatal(err)
	}
	if n := len(got.Message); n > maxMessage || n < maxMessage-3 {
		t.Errorf("sent %d bytes", n)
	}
}

func TestNotifyErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		header    string
		body      string
		key       i18n.Key
		permanent bool
		delay     time.Duration
	}{
		{403, "", `{"code":40301,"http":403,"error":"forbidden","link":"https://ntfy.sh/docs/publish/#authentication"}`, i18n.NtfyErrorAuth, true, 0},
		{401, "", `{"code":40101,"http":401,"error":"unauthorized"}`, i18n.NtfyErrorAuth, true, 0},
		{429, "", `{"code":42901,"http":429,"error":"limit reached: too many requests"}`, i18n.NtfyErrorThrottled, false, 0},
		{429, "30", `{"code":42908,"http":429,"error":"limit reached: daily message quota reached"}`, i18n.NtfyErrorThrottled, false, 30 * time.Second},
		{400, "", `{"code":40007,"http":400,"error":"invalid request: message too large"}`, i18n.NtfyErrorApi, true, 0},
		{507, "", `{"code":50701,"http":507,"error":"insufficient storage"}`, i18n.NtfyErrorApi, false, 0},
		{502, "", `<html>bad gateway</html>`, i18n.NtfyErrorStatus, false, 0},
		{404, "", `404 page not found`, i18n.NtfyErrorStatus, true, 0},
		{308, "", ``, i18n.NtfyErrorRedirect, true, 0},
	} {
		tp := serve(t, nil, func(w http.ResponseWriter, _ *http.Request) {
			if c.header != "" {
				w.Header().Set("Retry-After", c.header)
			}
			if c.status == 308 {
				w.Header().Set("Location", "https://ntfy.example.com/")
			}
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		})
		err := tp.Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
		if d, ok := retry.Delay(err); ok != (c.delay != 0) || d != c.delay {
			t.Errorf("%d %s: delay %v %v", c.status, c.body, d, ok)
		}
	}
}

func TestSettings(t *testing.T) {
	for _, server := range []string{"ntfy.sh", "ftp://ntfy.sh", "https://ntfy.sh/?x=1"} {
		_, err := factory(t.Context(), providers.Instance{Config: map[string]string{"server": server}})
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.NtfyErrorServer || !retry.IsPermanent(err) {
			t.Errorf("%q: %v", server, err)
		}
	}
	if tp := open(t, nil, nil); tp.server.String() != defaultServer {
		t.Errorf("server %s", tp.server)
	}
	for _, bad := range []string{"", "has space", "a/b", strings.Repeat("x", 65)} {
		tp := open(t, nil, nil)
		tp.name = bad
		err := tp.Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.NtfyErrorTopic || !retry.IsPermanent(err) {
			t.Errorf("topic %q: %v", bad, err)
		}
	}
}

func TestPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("reached") }))
	defer srv.Close()
	err := open(t, map[string]string{"server": srv.URL}, nil).Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.NtfyErrorPrivate || !retry.IsPermanent(err) {
		t.Errorf("a private address: %v", err)
	}
}

func TestNotifyNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	tp := open(t, map[string]string{"server": srv.URL, "allow_private": "true"}, nil)
	err := tp.Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.NtfyErrorNetwork || retry.IsPermanent(err) {
		t.Fatalf("no server: %v", err)
	}
}

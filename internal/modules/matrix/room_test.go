package matrix

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

func open(t *testing.T, server, roomName string) *room {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"server": server, "room": roomName, "allow_private": "true"}, Secrets: map[string]string{"token": "syt_secret"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*room)
}

func TestNotify(t *testing.T) {
	var sent []map[string]any
	var paths []string
	lookups := 0
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer syt_secret" {
			rw.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(rw, `{"errcode": "M_UNKNOWN_TOKEN", "error": "Unknown access token"}`)
			return
		}
		paths = append(paths, r.Method+" "+r.URL.EscapedPath())
		if strings.HasPrefix(r.URL.Path, "/_matrix/client/v3/directory/room/") {
			lookups++
			_, _ = io.WriteString(rw, `{"room_id": "!abc:example.org", "servers": ["example.org"]}`)
			return
		}
		var c map[string]any
		_ = json.NewDecoder(r.Body).Decode(&c)
		sent = append(sent, c)
		_, _ = io.WriteString(rw, `{"event_id": "$1"}`)
	}))
	defer srv.Close()

	r := open(t, srv.URL, "#certs:example.org")
	n := contracts.Note{Title: "Issued", Subject: "[JEC] Issued", Text: "a <b>\nb", Link: "https://jec.example/c?a=1&b=2", Time: time.Unix(1, 0)}
	for range 2 {
		if err := r.Notify(t.Context(), n); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 1 || len(sent) != 2 || !strings.HasPrefix(paths[1], "PUT /_matrix/client/v3/rooms/%21abc:example.org/send/m.room.message/jec") || paths[1] == paths[2] {
		t.Errorf("paths %v", paths)
	}
	c := sent[0]
	if c["msgtype"] != "m.notice" || c["body"] != "[JEC] Issued\na <b>\nb\nhttps://jec.example/c?a=1&b=2" ||
		c["formatted_body"] != `<strong>[JEC] Issued</strong><br>a &lt;b&gt;<br>b<br><a href="https://jec.example/c?a=1&amp;b=2">https://jec.example/c?a=1&amp;b=2</a>` {
		t.Errorf("sent %v", c)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{401, `{"errcode": "M_UNKNOWN_TOKEN", "error": "Unknown access token"}`, i18n.MatrixErrorToken, true},
		{403, `{"errcode": "M_FORBIDDEN", "error": "User not in room"}`, i18n.MatrixErrorForbidden, true},
		{429, `{"errcode": "M_LIMIT_EXCEEDED", "retry_after_ms": 2000}`, i18n.MatrixErrorApi, false},
		{502, ``, i18n.MatrixErrorApi, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(c.status)
			_, _ = io.WriteString(rw, c.body)
		}))
		err := open(t, srv.URL, "!abc:example.org").Notify(t.Context(), contracts.Note{Title: "t"})
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d: %v", c.status, err)
		}
		if d, ok := retry.Delay(err); c.status == 429 && (!ok || d != 2*time.Second) {
			t.Errorf("429: delay %v %v", d, ok)
		}
		srv.Close()
	}
	// An alias that names no room.
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(rw, `{"errcode": "M_NOT_FOUND", "error": "Room alias not found"}`)
	}))
	defer srv.Close()
	var p *contracts.Problem
	if err := open(t, srv.URL, "#nope:example.org").Notify(t.Context(), contracts.Note{Title: "t"}); !errors.As(err, &p) || p.Key != i18n.MatrixErrorRoom {
		t.Errorf("an unknown alias: %v", err)
	}
	for _, cfg := range []map[string]string{{"server": "ftp://x", "room": "!a:b"}, {"server": "https://x", "room": "room"}, {"server": "https://x", "room": "!ab"}} {
		if _, err := factory(t.Context(), providers.Instance{Config: cfg}); err == nil || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", cfg, err)
		}
	}
}

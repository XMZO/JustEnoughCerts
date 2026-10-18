package qiniu

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

const (
	accessKey = "ak"
	secretKey = "sk"
)

// fakeQiniu answers as Qiniu's API does, for what JEC uses.
type fakeQiniu struct {
	t       *testing.T
	mu      sync.Mutex
	certs   map[string]map[string]string // by ID: name, ca, pri
	order   []string
	domains map[string]*domain
	puts    []string // path and certificate ID
	deleted []string
	// inUse lists the certificates Qiniu refuses to delete.
	inUse map[string]bool
	reply func(method, path string) (int, string)
}

func (f *fakeQiniu) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mac := hmac.New(sha1.New, []byte(secretKey))
	mac.Write([]byte(r.URL.RequestURI() + "\n"))
	if want := "QBox " + accessKey + ":" + base64.URLEncoding.EncodeToString(mac.Sum(nil)); r.Header.Get("Authorization") != want {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"code": 401, "error": "bad token"}`)
		return
	}
	if f.reply != nil {
		if status, answer := f.reply(r.Method, r.URL.Path); status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, answer)
			return
		}
	}
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"code": %d, "error": %q}`, status, message)
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/sslcert":
		// One certificate a page, to go through the pages.
		var page struct {
			Marker string              `json:"marker"`
			Certs  []map[string]string `json:"certs"`
		}
		page.Certs = []map[string]string{}
		start := 0
		if m := r.URL.Query().Get("marker"); m != "" {
			_, _ = fmt.Sscan(m, &start)
		}
		if start < len(f.order) {
			id := f.order[start]
			page.Certs = append(page.Certs, map[string]string{"certid": id, "name": f.certs[id]["name"]})
			if start+1 < len(f.order) {
				page.Marker = fmt.Sprint(start + 1)
			}
		}
		_ = json.NewEncoder(w).Encode(page)
	case r.Method == http.MethodPost && path == "/sslcert":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := fmt.Sprintf("cert%d", len(f.order)+1)
		f.certs[id] = body
		f.order = append(f.order, id)
		_, _ = fmt.Fprintf(w, `{"code": 200, "error": "", "certID": %q}`, id)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/sslcert/"):
		id := strings.TrimPrefix(path, "/sslcert/")
		switch {
		case f.certs[id] == nil:
			fail(404, "cert not found")
		case f.inUse[id]:
			fail(400, "cert is in use")
		default:
			delete(f.certs, id)
			f.deleted = append(f.deleted, id)
			_, _ = io.WriteString(w, `{"code": 200, "error": ""}`)
		}
	case strings.HasPrefix(path, "/domain/"):
		parts := strings.Split(strings.TrimPrefix(path, "/domain/"), "/")
		d := f.domains[parts[0]]
		if d == nil {
			fail(404, "no such domain")
			return
		}
		if r.Method == http.MethodGet && len(parts) == 1 {
			_ = json.NewEncoder(w).Encode(d)
			return
		}
		var conf domainHTTPS
		_ = json.NewDecoder(r.Body).Decode(&conf)
		f.puts = append(f.puts, path+" "+conf.CertID)
		if parts[1] == "sslize" {
			d.Protocol = "https"
		}
		d.HTTPS = conf
		_, _ = io.WriteString(w, `{"code": 200, "error": ""}`)
	default:
		fail(404, "not found")
	}
}

func serve(t *testing.T, f *fakeQiniu, config map[string]string) *cdn {
	t.Helper()
	f.t = t
	if f.certs == nil {
		f.certs = map[string]map[string]string{}
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"access_key": accessKey, "secret_key": secretKey}})
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*cdn)
	c.base, c.client = srv.URL, srv.Client()
	return c
}

// request makes a certificate for example.com and *.example.com, logging
// into lines.
func request(t *testing.T, lines *[]contracts.LogLine) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com", "*.example.com")
	return contracts.DeployRequest{CertificateID: 7, Domains: []string{"example.com", "*.example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
		Log: func(l contracts.LogLine) { *lines = append(*lines, l) }}
}

func TestDeploy(t *testing.T) {
	f := &fakeQiniu{domains: map[string]*domain{
		"cdn.example.com": {Name: "cdn.example.com", Protocol: "https", HTTPS: domainHTTPS{CertID: "old", ForceHTTPS: true}},
		"img.example.com": {Name: "img.example.com", Protocol: "http"},
	}, inUse: map[string]bool{"other": true}}
	f.certs = map[string]map[string]string{"old": {"name": "jec-7-old"}, "other": {"name": "jec-7-other"}}
	f.order = []string{"old", "other"}
	c := serve(t, f, map[string]string{"domains": "cdn.example.com, IMG.example.com."})
	var lines []contracts.LogLine
	req := request(t, &lines)
	req.Previous = []string{"old", "other", "gone"}
	res, err := c.Deploy(t.Context(), req)
	if err != nil || res.Ref != "cert3" {
		t.Fatalf("%+v %v", res, err)
	}
	stored := f.certs["cert3"]
	b, err := cryptox.ParseBundle([]byte(stored["ca"]), []byte(stored["pri"]))
	if err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 || stored["common_name"] != "example.com" ||
		stored["name"] != "jec-7-"+cryptox.Fingerprint(req.Chain[0])[:12] {
		t.Errorf("stored %v: %v", stored, err)
	}
	if fmt.Sprint(f.puts) != "[/domain/cdn.example.com/httpsconf cert3 /domain/img.example.com/sslize cert3]" {
		t.Errorf("put %v", f.puts)
	}
	// Turning HTTPS on keeps it off by force; switching keeps the settings.
	if d := f.domains["cdn.example.com"].HTTPS; !d.ForceHTTPS {
		t.Errorf("cdn: %+v", d)
	}
	if d := f.domains["img.example.com"].HTTPS; d.ForceHTTPS || !d.HTTP2Enable {
		t.Errorf("img: %+v", d)
	}
	// The old one goes; the one in use stays, to try again next time; the
	// one gone already counts as removed.
	if fmt.Sprint(res.Removed) != "[old gone]" || fmt.Sprint(f.deleted) != "[old]" {
		t.Errorf("removed %v, deleted %v", res.Removed, f.deleted)
	}
	var warned bool
	for _, l := range lines {
		warned = warned || (l.Key == i18n.QiniuLogRemoveFailed && l.Params["id"] == "other" && l.Detail != "")
	}
	if !warned {
		t.Errorf("logged %+v", lines)
	}

	// Again, as after a failure: the certificate stored is found, across
	// pages, and the domains have it already.
	f.puts, lines = nil, nil
	req.Previous = nil
	if res, err = c.Deploy(t.Context(), req); err != nil || res.Ref != "cert3" || len(f.order) != 3 || len(f.puts) != 0 {
		t.Fatalf("again: %+v %v, %d stored, put %v", res, err, len(f.order), f.puts)
	}
	if len(lines) != 2 || lines[0].Key != i18n.QiniuLogCurrent {
		t.Errorf("logged %+v", lines)
	}
}

func TestOwnDomains(t *testing.T) {
	f := &fakeQiniu{domains: map[string]*domain{"example.com": {Name: "example.com", Protocol: "https"}}}
	var lines []contracts.LogLine
	res, err := serve(t, f, map[string]string{"delete_old": "false"}).Deploy(t.Context(), request(t, &lines))
	if err != nil || res.Ref != "cert1" || len(res.Removed) != 0 || fmt.Sprint(f.puts) != "[/domain/example.com/httpsconf cert1]" {
		t.Fatalf("%+v %v %v", res, err, f.puts)
	}
	req := request(t, &lines)
	req.Domains = []string{"*.example.com"}
	_, err = serve(t, f, nil).Deploy(t.Context(), req)
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.QiniuErrorNoDomains || !retry.IsPermanent(err) {
		t.Errorf("wildcards only: %v", err)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		method, path string
		status       int
		body         string
		key          i18n.Key
		permanent    bool
	}{
		{"POST", "/sslcert", 401, `{"code": 401, "error": "bad token"}`, i18n.QiniuErrorKeys, true},
		{"POST", "/sslcert", 400, `{"code": 400, "error": "invalid certificate"}`, i18n.QiniuErrorApi, true},
		{"PUT", "/domain/example.com/httpsconf", 500, `{"code": 500, "error": "internal"}`, i18n.QiniuErrorApi, false},
		{"PUT", "/domain/example.com/httpsconf", 502, `<html>bad gateway</html>`, i18n.QiniuErrorStatus, false},
	} {
		f := &fakeQiniu{domains: map[string]*domain{"example.com": {Name: "example.com", Protocol: "https"}},
			reply: func(method, path string) (int, string) {
				if method == c.method && path == c.path {
					return c.status, c.body
				}
				return 0, ""
			}}
		var lines []contracts.LogLine
		_, err := serve(t, f, nil).Deploy(t.Context(), request(t, &lines))
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%s %s %d: %v", c.method, c.path, c.status, err)
		}
	}
	var lines []contracts.LogLine
	_, err := serve(t, &fakeQiniu{domains: map[string]*domain{}}, map[string]string{"domains": "nope.example.com"}).Deploy(t.Context(), request(t, &lines))
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.QiniuErrorDomain || pr.Params["domain"] != "nope.example.com" || !retry.IsPermanent(err) {
		t.Errorf("no such domain: %v", err)
	}
}

func TestTest(t *testing.T) {
	f := &fakeQiniu{domains: map[string]*domain{"cdn.example.com": {Name: "cdn.example.com"}}}
	msg, err := serve(t, f, map[string]string{"domains": "cdn.example.com"}).Test(t.Context())
	if err != nil || msg.Key != i18n.QiniuTestOk || msg.Params["domains"] != "cdn.example.com" {
		t.Errorf("domains: %+v %v", msg, err)
	}
	msg, err = serve(t, f, nil).Test(t.Context())
	if err != nil || msg.Key != i18n.QiniuTestKeys {
		t.Errorf("keys: %+v %v", msg, err)
	}
	c := serve(t, f, nil)
	c.secretKey = []byte("wrong")
	_, err = c.Test(t.Context())
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.QiniuErrorKeys || !retry.IsPermanent(err) {
		t.Errorf("wrong key: %v", err)
	}
}

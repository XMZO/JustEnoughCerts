package esa

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alibabacloud-go/tea/dara"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// fakeEdge is an account of ESA in memory, with what answers at its edge:
// a request is answered from Edge KV when its site routes it to JEC's
// routine, running JEC's code.
type fakeEdge struct {
	mu         sync.Mutex
	siteList   []site
	namespaces []string
	kv         map[string][]byte
	routines   map[string]*fakeRoutine
	routeList  []route
	next       int64
	calls      []string
	// fail holds the errors of calls, by name; raced makes createRoutine
	// fail as if another installation had just created the routine.
	fail  map[string]error
	raced bool
	// lag is how many fetches the edge does not answer yet.
	lag int
}

type fakeRoutine struct {
	versions []codeVersion
	deployed string
}

func newFakeEdge() *fakeEdge {
	return &fakeEdge{
		siteList: []site{{ID: 7, Name: "example.com"}, {ID: 8, Name: "other.example"}},
		kv:       map[string][]byte{}, routines: map[string]*fakeRoutine{}, fail: map[string]error{},
	}
}

func sdkError(code string, status int) error {
	return &dara.SDKError{Code: dara.String(code), StatusCode: dara.Int(status)}
}

// call records a call, and returns the error it is set to fail with.
func (f *fakeEdge) call(name string, args ...any) error {
	f.calls = append(f.calls, strings.TrimSpace(name+" "+fmt.Sprint(args...)))
	return f.fail[name]
}

func (f *fakeEdge) sites(context.Context) ([]site, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("sites"); err != nil {
		return nil, err
	}
	return slices.Clone(f.siteList), nil
}

func (f *fakeEdge) namespace(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("namespace"); err != nil {
		return err
	}
	if !slices.Contains(f.namespaces, name) {
		return sdkError("InvalidNameSpace.NotFound", http.StatusNotFound)
	}
	return nil
}

func (f *fakeEdge) createNamespace(_ context.Context, name, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("createNamespace"); err != nil {
		return err
	}
	if slices.Contains(f.namespaces, name) || description == "" {
		return sdkError("InvalidNameSpace.Duplicate", http.StatusBadRequest)
	}
	f.namespaces = append(f.namespaces, name)
	return nil
}

func (f *fakeEdge) putKV(_ context.Context, namespace, key string, value []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("putKV", key); err != nil {
		return err
	}
	if !slices.Contains(f.namespaces, namespace) || ttl < time.Hour {
		return sdkError("InvalidNameSpace.NotFound", http.StatusNotFound)
	}
	f.kv[key] = slices.Clone(value)
	return nil
}

func (f *fakeEdge) deleteKV(_ context.Context, _, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("deleteKV", key); err != nil {
		return err
	}
	delete(f.kv, key)
	return nil
}

func (f *fakeEdge) routine(_ context.Context, name string) ([]codeVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("routine"); err != nil {
		return nil, err
	}
	r := f.routines[name]
	if r == nil {
		return nil, sdkError("RoutineNotExist", http.StatusBadRequest)
	}
	return slices.DeleteFunc(slices.Clone(r.versions), func(v codeVersion) bool { return v.Version != r.deployed }), nil
}

func (f *fakeEdge) createRoutine(_ context.Context, name, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("createRoutine"); err != nil {
		return err
	}
	if f.routines[name] != nil {
		return sdkError("RoutineAlreadyExist", http.StatusBadRequest)
	}
	f.routines[name] = &fakeRoutine{}
	if f.raced {
		return sdkError("RoutineAlreadyExist", http.StatusBadRequest)
	}
	return nil
}

func (f *fakeEdge) codeVersions(_ context.Context, name string) ([]codeVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("codeVersions"); err != nil {
		return nil, err
	}
	return slices.Clone(f.routines[name].versions), nil
}

func (f *fakeEdge) uploadCode(_ context.Context, name, description string, code []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("uploadCode"); err != nil {
		return "", err
	}
	r := f.routines[name]
	if len(r.versions) >= 10 {
		return "", sdkError("CodeVersion.ExceedQuota", http.StatusBadRequest)
	}
	if want, _ := routineCode(); !bytes.Equal(code, want) {
		return "", errors.New("not the routine's code")
	}
	f.next++
	version := strconv.FormatInt(f.next, 10)
	r.versions = append(r.versions, codeVersion{Version: version, Description: description})
	return version, nil
}

func (f *fakeEdge) deployCode(_ context.Context, name, version string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("deployCode", version); err != nil {
		return err
	}
	r := f.routines[name]
	if !slices.ContainsFunc(r.versions, func(v codeVersion) bool { return v.Version == version }) {
		return sdkError("CodeVersionNotExist", http.StatusBadRequest)
	}
	r.deployed = version
	return nil
}

func (f *fakeEdge) deleteCode(_ context.Context, name, version string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("deleteCode", version); err != nil {
		return err
	}
	r := f.routines[name]
	if r.deployed == version {
		return sdkError("CodeVersionInUse", http.StatusBadRequest)
	}
	r.versions = slices.DeleteFunc(r.versions, func(v codeVersion) bool { return v.Version == version })
	return nil
}

func (f *fakeEdge) routes(_ context.Context, routine string) ([]route, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("routes"); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(f.routeList), func(r route) bool { return r.Routine != routine }), nil
}

func (f *fakeEdge) setRoute(_ context.Context, r route) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("setRoute", r.ConfigID); err != nil {
		return err
	}
	r.Enabled, r.Bypass = true, false
	if r.ConfigID == 0 {
		f.next++
		r.ConfigID = f.next
		f.routeList = append(f.routeList, r)
		return nil
	}
	i := slices.IndexFunc(f.routeList, func(c route) bool { return c.ConfigID == r.ConfigID })
	if i < 0 {
		return sdkError("ConfigNotExist", http.StatusBadRequest)
	}
	f.routeList[i] = r
	return nil
}

// serve answers a request from the internet as the edge does, with the
// routine's file when it may, or else as the origin, which has none.
func (f *fakeEdge) serve(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := httptest.NewRecorder()
	value, ok := f.answer(r.URL.Hostname(), r.URL.Path)
	switch {
	case f.lag > 0:
		f.lag--
		http.NotFound(rec, r)
	case ok:
		_, _ = rec.Write(value)
	default:
		http.NotFound(rec, r)
	}
	return rec.Result(), nil
}

func (f *fakeEdge) answer(host, path string) ([]byte, bool) {
	var s site
	for _, c := range f.siteList {
		if (host == c.Name || strings.HasSuffix(host, "."+c.Name)) && len(c.Name) > len(s.Name) {
			s = c
		}
	}
	routed := slices.ContainsFunc(f.routeList, func(r route) bool {
		return r.SiteID == s.ID && r.Routine == routineName && sameRule(r.Rule, routeRule) && r.Enabled && !r.Bypass
	})
	rt := f.routines[routineName]
	if !routed || rt == nil || !strings.HasPrefix(path, "/.well-known/") {
		return nil, false
	}
	_, description := routineCode()
	if !slices.ContainsFunc(rt.versions, func(v codeVersion) bool {
		return v.Version == rt.deployed && sameDescription(v.Description, description)
	}) {
		return nil, false
	}
	key, ok := kvKey(host, path)
	value, found := f.kv[key]
	return value, ok && found
}

// roundTrip lets a function stand for the internet.
type roundTrip func(*http.Request) (*http.Response, error)

func (rt roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return rt(r) }

func newEdgeModule(f *fakeEdge) *Module {
	m := &Module{poll: time.Millisecond, propagate: 300 * time.Millisecond}
	m.check = &http.Client{Transport: roundTrip(f.serve)}
	m.newEdge = func(providers.Instance) (edgeAPI, error) { return f, nil }
	return m
}

func openEdge(t *testing.T, m *Module, domain string) *edge {
	t.Helper()
	p, err := m.edgeFactory(t.Context(), providers.Instance{Config: map[string]string{"domain": domain}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*edge)
}

func takeCalls(f *fakeEdge) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

func TestEdgeType(t *testing.T) {
	typ := edgeType()
	if typ.ID != "esa.edge" || typ.Kind != providers.Solver || typ.Challenge != providers.ChallengeHTTP || !typ.Testable || !typ.Fields[2].Secret {
		t.Fatalf("type %+v", typ)
	}
	if e := openEdge(t, newEdgeModule(newFakeEdge()), " WWW.Example.com "); e.domain != "www.example.com" {
		t.Errorf("domain %q", e.domain)
	}
}

func TestKVKey(t *testing.T) {
	for _, c := range []struct{ host, path, want string }{
		{"Example.com", "/.well-known/acme-challenge/a_b.c~d-E", "example_dcom_s_dwell-known_sacme-challenge_sa__b_dc_td-E"},
		{"xn--fiqs8s.example", "/.well-known/pki-validation/1A2B.txt", "xn--fiqs8s_dexample_s_dwell-known_spki-validation_s1A2B_dtxt"},
	} {
		if got, ok := kvKey(c.host, c.path); !ok || got != c.want {
			t.Errorf("kvKey(%q, %q) = %q, %v", c.host, c.path, got, ok)
		}
	}
	for _, path := range []string{"/.well-known/a%20b", "/.well-known/" + strings.Repeat("a", maxKey)} {
		if got, ok := kvKey("example.com", path); ok {
			t.Errorf("kvKey(%q) = %q", path, got)
		}
	}
}

func TestEdgePutFile(t *testing.T) {
	f := newFakeEdge()
	m := newEdgeModule(f)
	e := openEdge(t, m, "")
	file := contracts.File{Domain: "WWW.Example.com", Path: "/.well-known/acme-challenge/token_1", Content: []byte("key-auth")}
	key := "www_dexample_dcom_s_dwell-known_sacme-challenge_stoken__1"
	if err := e.PutFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	want := []string{"sites", "namespace", "createNamespace", "routine", "createRoutine", "codeVersions", "uploadCode",
		"deployCode 1", "routes", "setRoute 0", "putKV " + key}
	if calls := takeCalls(f); !slices.Equal(calls, want) {
		t.Errorf("set up with %q", calls)
	}
	if len(f.routeList) != 1 || f.routeList[0].SiteID != 7 || f.routeList[0].Name != routeName || f.routeList[0].Rule != routeRule {
		t.Errorf("routes %+v", f.routeList)
	}

	// The instance has set it all up: more files need nothing more, but
	// for a site of its own, its route.
	if err := e.PutFile(t.Context(), contracts.File{Domain: "example.com", Path: "/.well-known/pki-validation/x.txt", Content: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := e.PutFile(t.Context(), contracts.File{Domain: "a.other.example", Path: "/.well-known/x", Content: []byte("y")}); err != nil {
		t.Fatal(err)
	}
	want = []string{"putKV example_dcom_s_dwell-known_spki-validation_sx_dtxt", "routes", "setRoute 0", "putKV a_dother_dexample_s_dwell-known_sx"}
	if calls := takeCalls(f); !slices.Equal(calls, want) {
		t.Errorf("then %q", calls)
	}

	// Another instance finds it all in place.
	if err := openEdge(t, m, "").PutFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if calls := takeCalls(f); !slices.Equal(calls, []string{"sites", "namespace", "routine", "routes", "putKV " + key}) {
		t.Errorf("another instance %q", calls)
	}

	for range 2 { // gone already the second time
		if err := e.RemoveFile(t.Context(), file); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := f.kv[key]; ok {
		t.Error("the file is still there")
	}
}

func TestEdgeReplacesCode(t *testing.T) {
	_, description := routineCode()
	f := newFakeEdge()
	f.namespaces = []string{kvNamespace}
	f.routines[routineName] = &fakeRoutine{
		versions: []codeVersion{{"1", "jec 0000000000000000"}, {"2", "jec 1111111111111111"}, {"3", "jec 2222222222222222"}},
		deployed: "3",
	}
	f.next = 3
	f.routeList = []route{{ConfigID: 9, SiteID: 7, Name: routeName, Routine: routineName, Rule: routeRule, Enabled: true}}
	file := contracts.File{Domain: "example.com", Path: "/.well-known/x", Content: []byte("x")}
	if err := openEdge(t, newEdgeModule(f), "").PutFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	want := []string{"sites", "namespace", "routine", "codeVersions", "deleteCode 1", "deleteCode 2", "uploadCode",
		"deployCode 4", "deleteCode 3", "routes", "putKV example_dcom_s_dwell-known_sx"}
	if calls := takeCalls(f); !slices.Equal(calls, want) {
		t.Errorf("calls %q", calls)
	}
	if v := f.routines[routineName].versions; len(v) != 1 || v[0].Description != description {
		t.Errorf("versions %+v", v)
	}

	// A version uploaded before but not deployed is deployed. ESA may give
	// the description back in Base64.
	f.routines[routineName] = &fakeRoutine{versions: []codeVersion{{"5", base64.StdEncoding.EncodeToString([]byte(description))}}}
	if err := openEdge(t, newEdgeModule(f), "").PutFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if calls := takeCalls(f); !slices.Equal(calls, []string{"sites", "namespace", "routine", "codeVersions", "deployCode 5", "routes", "putKV example_dcom_s_dwell-known_sx"}) {
		t.Errorf("deploying %q", calls)
	}
}

func TestEdgeFixesRoute(t *testing.T) {
	f := newFakeEdge()
	m := newEdgeModule(f)
	file := contracts.File{Domain: "example.com", Path: "/.well-known/x", Content: []byte("x")}
	if err := openEdge(t, m, "").PutFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	id := f.routeList[0].ConfigID
	takeCalls(f)

	// Written back otherwise, the rule is the same.
	f.routeList[0].Rule = strings.ReplaceAll(strings.ReplaceAll(routeRule, " ", ""), `"`, `\"`)
	if err := openEdge(t, m, "").PutFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if calls := takeCalls(f); slices.Contains(calls, fmt.Sprint("setRoute ", id)) {
		t.Errorf("the same rule was set again: %q", calls)
	}

	// Someone bypassed the routine and changed the rule.
	f.routeList[0].Bypass, f.routeList[0].Rule = true, `(http.host eq "example.com")`
	if err := openEdge(t, m, "").PutFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if calls := takeCalls(f); !slices.Contains(calls, fmt.Sprint("setRoute ", id)) {
		t.Errorf("calls %q", calls)
	}
	if r := f.routeList; len(r) != 1 || r[0].Bypass || r[0].Rule != routeRule {
		t.Errorf("routes %+v", r)
	}
}

func TestEdgeCreateRaced(t *testing.T) {
	f := newFakeEdge()
	f.raced = true
	if err := openEdge(t, newEdgeModule(f), "").PutFile(t.Context(), contracts.File{Domain: "example.com", Path: "/.well-known/x", Content: []byte("x")}); err != nil {
		t.Fatal(err)
	}
}

func problemOf(err error) *contracts.Problem {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p
	}
	return nil
}

func TestEdgeFailures(t *testing.T) {
	file := contracts.File{Domain: "www.example.com", Path: "/.well-known/acme-challenge/t", Content: []byte("x")}
	for _, c := range []struct {
		name      string
		set       func(*fakeEdge)
		file      contracts.File
		key       i18n.Key
		permanent bool
	}{
		{"no site", nil, contracts.File{Domain: "example.net", Path: file.Path}, i18n.EsaErrorNoSite, true},
		{"outside /.well-known/", nil, contracts.File{Domain: "example.com", Path: "/index.html"}, i18n.EsaErrorFilePath, true},
		{"not activated", func(f *fakeEdge) { f.fail["createRoutine"] = sdkError("ServiceNotOpen", 400) }, file, i18n.EsaErrorService, true},
		{"routines at their limit", func(f *fakeEdge) { f.fail["createRoutine"] = sdkError("RoutineNumberExceedQuotaLimit", 400) }, file, i18n.EsaErrorLimit, true},
		{"rules the plan lacks", func(f *fakeEdge) { f.fail["setRoute"] = sdkError("FunctionQuotaCheckFailed", 400) }, file, i18n.EsaErrorLimit, true},
		{"refused key", func(f *fakeEdge) { f.fail["sites"] = sdkError("InvalidAccessKeyId.NotFound", 404) }, file, i18n.EsaErrorAuth, true},
		{"throttled", func(f *fakeEdge) { f.fail["putKV"] = sdkError("Throttling.User", 400) }, file, i18n.EsaErrorThrottled, false},
		{"other", func(f *fakeEdge) { f.fail["routes"] = sdkError("CompileRuleError", 400) }, file, i18n.EsaErrorApi, false},
		{"never served", func(f *fakeEdge) { f.lag = 1 << 30 }, file, i18n.EsaErrorNotServed, false},
	} {
		f := newFakeEdge()
		if c.set != nil {
			c.set(f)
		}
		err := openEdge(t, newEdgeModule(f), "").PutFile(t.Context(), c.file)
		if p := problemOf(err); p == nil || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestEdgeWaits(t *testing.T) {
	f := newFakeEdge()
	f.lag = 5
	if err := openEdge(t, newEdgeModule(f), "").PutFile(t.Context(), contracts.File{Domain: "example.com", Path: "/.well-known/x", Content: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if f.lag != 0 {
		t.Errorf("returned before the edge answered: %d", f.lag)
	}

	f.lag = 1 << 30
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := openEdge(t, newEdgeModule(f), "").PutFile(ctx, contracts.File{Domain: "example.com", Path: "/.well-known/y", Content: []byte("y")}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
}

func TestEdgeTest(t *testing.T) {
	f := newFakeEdge()
	m := newEdgeModule(f)
	if _, err := openEdge(t, m, "").Test(t.Context()); problemOf(err) == nil || problemOf(err).Key != i18n.EsaErrorNoDomain || !retry.IsPermanent(err) {
		t.Errorf("no domain: %v", err)
	}
	msg, err := openEdge(t, m, "example.com").Test(t.Context())
	if err != nil || msg.Key != i18n.EsaTestEdgeOk || !strings.HasPrefix(msg.Params["url"].(string), "http://example.com/.well-known/jec-check/") {
		t.Fatalf("test: %+v %v", msg, err)
	}
	if len(f.kv) != 0 {
		t.Errorf("left %v", slices.Collect(maps.Keys(f.kv)))
	}

	// The deadline of the test bounds the wait, with time to report.
	f.lag = 1 << 30
	m.propagate = time.Hour
	ctx, cancel := context.WithTimeout(t.Context(), reportMargin+500*time.Millisecond)
	defer cancel()
	if _, err := openEdge(t, m, "example.com").Test(ctx); problemOf(err) == nil || problemOf(err).Key != i18n.EsaErrorNotServed {
		t.Errorf("not served: %v", err)
	}
}

func TestPost(t *testing.T) {
	type part struct{ name, value string }
	var (
		mu     sync.Mutex // the server's goroutine and the test's share:
		parts  []part
		status = http.StatusNoContent
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		parts = nil
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(p)
			parts = append(parts, part{p.FormName(), string(data)})
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	posted := func() []part {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(parts)
	}
	s := &sdk{web: srv.Client()}
	config := map[string]any{
		// Over HTTPS, whatever the address says.
		"Url":            strings.Replace(srv.URL, "https://", "http://", 1),
		"OSSAccessKeyId": "id", "policy": "p", "Signature": "sig", "key": "sp_esa/x/index.js", "callback": "cb",
		"x:codeDescription": "jec 1", "XOssSecurityToken": "token",
	}
	if err := s.post(t.Context(), config, []byte("code")); err != nil {
		t.Fatal(err)
	}
	want := []part{{"OSSAccessKeyId", "id"}, {"Signature", "sig"}, {"x-oss-security-token", "token"}, {"callback", "cb"},
		{"key", "sp_esa/x/index.js"}, {"policy", "p"}, {"x:codeDescription", "jec 1"}, {"file", "code"}}
	if got := posted(); !slices.Equal(got, want) {
		t.Errorf("posted %q", got)
	}

	config["XOssSecurityToken"] = ""
	mu.Lock()
	status = http.StatusNonAuthoritativeInfo // OSS's word for a failed callback to ESA
	mu.Unlock()
	if err := s.post(t.Context(), config, []byte("code")); err == nil || !strings.Contains(err.Error(), "203") {
		t.Errorf("a failed callback: %v", err)
	}
	if slices.ContainsFunc(posted(), func(p part) bool { return p.name == "x-oss-security-token" }) {
		t.Error("an empty token was sent")
	}
	if err := s.post(t.Context(), map[string]any{"key": "x"}, nil); err == nil {
		t.Error("no address")
	}
}

func TestSameRule(t *testing.T) {
	if !sameRule(routeRule, `(starts_with(http.request.uri.path,\"/.well-known/\"))`) || sameRule(routeRule, `(http.host eq "example.com")`) {
		t.Error("sameRule")
	}
}

// routineDriver runs the routine in Node.js, with Edge KV and the origin
// stubbed: it answers the requests read from stdin, and writes what it
// asked Edge KV for and the answers.
const routineDriver = `import { pathToFileURL } from "node:url";

let text = "";
for await (const chunk of process.stdin) text += chunk;
const input = JSON.parse(text);
const asked = [];
globalThis.EdgeKV = class {
  constructor(options) {
    this.namespace = options.namespace;
  }
  async get(key, options) {
    asked.push([this.namespace, key, options.type]);
    if (key.startsWith("kv-down")) throw new Error("Edge KV is down");
    const value = input.store[key];
    return value === undefined ? undefined : new TextEncoder().encode(value).buffer;
  }
};
globalThis.fetch = async () => new Response("origin", { status: 404 });
const routine = (await import(pathToFileURL(process.argv[2]).href)).default;
const answers = [];
for (const [method, url] of input.requests) {
  const response = await routine.fetch(new Request(url, { method }));
  answers.push({ status: response.status, body: await response.text(), cache: response.headers.get("cache-control") || "" });
}
process.stdout.write(JSON.stringify({ asked, answers }));
`

// TestRoutine runs routine.js, where Node.js is installed, to check that it
// asks Edge KV for the keys kvKey makes.
func TestRoutine(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no Node.js to run the routine with")
	}
	dir := t.TempDir()
	code, _ := routineCode()
	if err := os.WriteFile(filepath.Join(dir, "routine.mjs"), code, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "driver.mjs"), []byte(routineDriver), 0o600); err != nil {
		t.Fatal(err)
	}
	token := "/.well-known/acme-challenge/tok_en.1~x"
	answered, _ := kvKey("example.com", token)
	missing, _ := kvKey("example.com", "/.well-known/acme-challenge/missing")
	down, _ := kvKey("kv-down.example", "/.well-known/x")
	input, _ := json.Marshal(map[string]any{
		"store": map[string]string{answered: "key-auth"},
		"requests": [][2]string{
			{"GET", "http://Example.COM" + token},
			{"HEAD", "http://example.com" + token},
			{"GET", "http://example.com/.well-known/acme-challenge/missing"},
			{"POST", "http://example.com" + token},
			{"GET", "http://example.com/.well-known/a%20b"},
			{"GET", "http://kv-down.example/.well-known/x"},
		},
	})
	cmd := exec.CommandContext(t.Context(), node, filepath.Join(dir, "driver.mjs"), filepath.Join(dir, "routine.mjs"))
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("%v: %s", err, exit.Stderr)
		}
		t.Fatal(err)
	}
	type answer struct {
		Status      int
		Body, Cache string
	}
	var got struct {
		Asked   [][3]string
		Answers []answer
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	wantAsked := [][3]string{
		{kvNamespace, answered, "arrayBuffer"}, {kvNamespace, answered, "arrayBuffer"},
		{kvNamespace, missing, "arrayBuffer"}, {kvNamespace, down, "arrayBuffer"},
	}
	if !slices.Equal(got.Asked, wantAsked) {
		t.Errorf("asked Edge KV for %q", got.Asked)
	}
	origin := answer{404, "origin", ""}
	wantAnswers := []answer{{200, "key-auth", "no-store"}, {200, "", "no-store"}, origin, origin, origin, origin}
	if !slices.Equal(got.Answers, wantAnswers) {
		t.Errorf("answered %+v", got.Answers)
	}
}

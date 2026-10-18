package plugins

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// The test binary is also the plugin of the tests: run with helperEnv set,
// it answers one request as a plugin, and describes itself as describeEnv
// says.
const (
	helperEnv   = "PLUGINS_TEST_HELPER"
	describeEnv = "PLUGINS_TEST_DESCRIBE"
)

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) != "" {
		os.Exit(helper())
	}
	os.Exit(m.Run())
}

// fakeDescription is what the plugin of the tests offers.
const fakeDescription = `{"protocol":1,"ok":true,
 "name":{"en-US":"Fake","zh-CN":"Fake (zh)"},
 "description":"A plugin of the tests",
 "types":[
  {"id":"deploy","kind":"target","testable":true,
   "name":{"en-US":"Fake target","zh-CN":"Fake target (zh)"},
   "description":"Deploys {nowhere}, doesn't it?",
   "fields":[
    {"key":"endpoint","kind":"url","label":"Endpoint","required":true},
    {"key":"token","kind":"text","label":{"en":"Token"},"secret":true},
    {"key":"mode","kind":"select","default":"a","options":[{"value":"a","label":"Mode A"},{"value":"b"}]},
    {"key":"extra","kind":"text","when":{"key":"mode","value":"b"}},
    {"key":"behave","kind":"text","advanced":true}]},
  {"id":"notes","kind":"notifier","name":"Fake notes","fields":[{"key":"behave","kind":"text"}]},
  {"id":"dns","kind":"solver","name":"Fake DNS","wait":{"timeout":30,"interval":1},"fields":[{"key":"behave","kind":"text"}]},
  {"id":"files","kind":"solver","challenge":"http","name":"Fake files","fields":[{"key":"behave","kind":"text"}]}]}`

// helper is the plugin of the tests. It keeps each request in its working
// directory, and behaves as its settings say.
func helper() int {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	raw, _ := json.Marshal(req)
	_ = os.WriteFile("request.json", raw, 0o600)
	behave := req.Config["behave"]
	if req.Action == actionDescribe {
		behave = os.Getenv(describeEnv)
	}
	reply := func(v any) int {
		_ = json.NewEncoder(os.Stdout).Encode(v)
		return 0
	}
	switch behave {
	case "garbage":
		fmt.Print("not JSON")
		return 0
	case "exit":
		fmt.Fprint(os.Stderr, "boom")
		return 3
	case "slow":
		time.Sleep(time.Minute)
		return 0
	case "protocol2":
		return reply(map[string]any{"protocol": 2, "ok": true})
	case "huge":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte(" "), maxAnswer+1))
		return 0
	case "fail":
		fmt.Fprint(os.Stderr, "the API said no")
		return reply(map[string]any{"protocol": 1, "ok": false, "error": "no luck", "log": []logLine{{Level: "error", Text: "refused"}}})
	case "retry":
		return reply(map[string]any{"protocol": 1, "ok": false, "error": "busy", "retry": true, "retry_after": 30})
	case "bad-kind":
		fmt.Print(strings.Replace(fakeDescription, `"kind":"notifier"`, `"kind":"source"`, 1))
		return 0
	}
	switch req.Action {
	case actionDescribe:
		fmt.Print(fakeDescription)
		return 0
	case actionTest:
		return reply(map[string]any{"protocol": 1, "ok": true, "message": "reached " + req.Config["endpoint"]})
	case actionDeploy:
		return reply(map[string]any{
			"protocol": 1, "ok": true, "ref": "v-" + req.Certificate.Serial, "removed": append(req.Previous, "not-one-of-them"),
			"log": []logLine{{Level: "ok", Text: "uploaded"}, {Level: "loud", Text: "done"}},
		})
	}
	return reply(map[string]any{"protocol": 1, "ok": true})
}

// install puts the plugin of the tests into the plugins directory of data,
// as name.
func install(t *testing.T, data, name string) {
	t.Helper()
	dir := filepath.Join(data, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, name)
	// Windows keeps a link to a running program from being removed.
	if runtime.GOOS != "windows" && os.Link(self, dst) == nil {
		return
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	t    *testing.T
	data string
	reg  *providers.Registry
	m    *Module
	api  humatest.TestAPI
}

// newFixture starts the module on a data directory where install put
// plugins.
func newFixture(t *testing.T, plugins ...string) *fixture {
	t.Helper()
	t.Setenv(helperEnv, "1")
	data := t.TempDir()
	for _, p := range plugins {
		install(t, data, p)
	}
	st, err := store.Open(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(data, "")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, data: data, m: New()}
	f.reg = providers.New(st.DB(), box.Scope("kernel"), func(string) bool { return true })
	_, f.api = humatest.New(t)
	r := &module.Registry{API: f.api, Log: slog.New(slog.DiscardHandler), Providers: f.reg.Scope("plugins"), DataDir: data}
	if err := f.m.Register(r); err != nil {
		t.Fatal(err)
	}
	f.reg.Seal()
	return f
}

// open configures an instance of type and opens it.
func (f *fixture) open(typ string, config, secrets map[string]string) any {
	f.t.Helper()
	id, err := f.reg.Create(f.t.Context(), typ, "test", config, secrets)
	if err != nil {
		f.t.Fatal(err)
	}
	p, _, err := f.reg.Scope("certs").Open(f.t.Context(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

// sent is the last request the plugin got.
func (f *fixture) sent() request {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.data, "plugins", "fake.data", "request.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		f.t.Fatal(err)
	}
	return req
}

func (f *fixture) list() PluginListBody {
	f.t.Helper()
	resp := f.api.Get("/files")
	if resp.Code != http.StatusOK {
		f.t.Fatalf("list: %d %s", resp.Code, resp.Body)
	}
	var body PluginListBody
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		f.t.Fatal(err)
	}
	return body
}

func TestDescribe(t *testing.T) {
	f := newFixture(t, "fake")
	ids := []string{"plugins.fake.deploy", "plugins.fake.notes", "plugins.fake.dns", "plugins.fake.files"}
	for _, id := range ids {
		if _, ok := f.reg.Type(id); !ok {
			t.Errorf("no type %s", id)
		}
	}
	typ, _ := f.reg.Type("plugins.fake.deploy")
	if typ.Name != "plugins.texts.fake.deploy.name" || typ.Kind != providers.Target || !typ.Testable || !slices.Equal(typ.Tags, []string{"plugin"}) {
		t.Errorf("type %+v", typ)
	}
	text := func(tag string, key i18n.Key) string {
		src, ok := typ.Texts[tag][key]
		if !ok {
			t.Errorf("no text %s in %s", key, tag)
			return ""
		}
		m, err := i18n.ParseMessage(src)
		if err != nil {
			t.Fatal(err)
		}
		s, err := m.Format(i18n.BaseLocale, nil)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, c := range []struct {
		tag  string
		key  i18n.Key
		want string
	}{
		{"zh-CN", typ.Name, "Fake target (zh)"},
		{"en-US", typ.Name, "Fake target"},
		{"zh-CN", typ.Description, "Deploys {nowhere}, doesn't it?"},
		{"zh-CN", typ.Fields[1].Label, "Token"},
		{"en-US", typ.Fields[2].Options[1].Label, "b"},
	} {
		if got := text(c.tag, c.key); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.tag, c.key, got, c.want)
		}
	}
	if typ.Fields[3].When == nil || typ.Fields[3].When.Key != "mode" || !typ.Fields[1].Secret || !typ.Fields[0].Required {
		t.Errorf("fields %+v", typ.Fields)
	}
	notes, _ := f.reg.Type("plugins.fake.notes")
	if !slices.ContainsFunc(notes.Fields, func(f providers.Field) bool { return f.Key == contracts.NoteLocale }) {
		t.Errorf("the notifier has no language: %+v", notes.Fields)
	}
	if dns, _ := f.reg.Type("plugins.fake.dns"); dns.Solves() != providers.ChallengeDNS {
		t.Errorf("dns solves %s", dns.Solves())
	}
	if files, _ := f.reg.Type("plugins.fake.files"); files.Solves() != providers.ChallengeHTTP {
		t.Errorf("files solve %s", files.Solves())
	}

	body := f.list()
	if body.Dir != filepath.Join(f.data, "plugins") || len(body.Plugins) != 1 {
		t.Fatalf("list %+v", body)
	}
	p := body.Plugins[0]
	if p.ID != "fake" || p.Error != nil || !slices.Equal(p.Types, ids) || p.Name != "plugins.texts.fake.name" || p.Texts["zh-CN"][p.Name] != "Fake (zh)" {
		t.Errorf("plugin %+v", p)
	}
}

func TestDescribeFails(t *testing.T) {
	for behave, key := range map[string]i18n.Key{
		"bad-kind":  i18n.PluginsErrorDescribe,
		"garbage":   i18n.PluginsErrorAnswer,
		"exit":      i18n.PluginsErrorExit,
		"protocol2": i18n.PluginsErrorAnswer,
		"fail":      i18n.PluginsErrorFailed,
	} {
		t.Setenv(describeEnv, behave)
		f := newFixture(t, "fake")
		body := f.list()
		if len(body.Plugins) != 1 || body.Plugins[0].Error == nil || body.Plugins[0].Error.Key != key || len(body.Plugins[0].Types) != 0 {
			t.Errorf("%s: %+v", behave, body.Plugins)
		}
		if _, ok := f.reg.Type("plugins.fake.deploy"); ok {
			t.Errorf("%s: the types are there", behave)
		}
	}
}

func TestScan(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "plugins")
	install(t, data, "Not_a-Name")
	install(t, data, ".hidden")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a program"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		install(t, data, "twice")
		if err := os.Link(filepath.Join(dir, "twice"), filepath.Join(dir, "twice.sh")); err != nil {
			t.Fatal(err)
		}
	}
	found, err := scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]i18n.Key{}
	for _, l := range found {
		var key i18n.Key
		if l.err != nil {
			key = l.err.Key
		}
		got[l.file] = key
	}
	want := map[string]i18n.Key{"Not_a-Name": i18n.PluginsErrorName}
	if runtime.GOOS == "windows" {
		want = map[string]i18n.Key{"Not_a-Name.exe": i18n.PluginsErrorName}
	} else {
		want["twice"], want["twice.sh"] = "", i18n.PluginsErrorRepeated
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("found %v, want %v", got, want)
	}
	if found, err := scan(filepath.Join(data, "none")); err != nil || found != nil {
		t.Errorf("no directory: %v %v", found, err)
	}
}

func TestUnsafe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions")
	}
	dir := t.TempDir()
	open := filepath.Join(dir, "open")
	if err := os.WriteFile(open, []byte("#!/bin/sh"+string(rune(10))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	found, err := scan(dir)
	if err != nil || len(found) != 1 || found[0].err == nil || found[0].err.Key != i18n.PluginsErrorWritable {
		t.Errorf("a file anyone may change: %+v %v", found, err)
	}
}

// request of the tests to deploy.
func deployRequest(t *testing.T) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com", "www.example.com")
	return contracts.DeployRequest{CertificateID: 7, Domains: []string{"example.com", "www.example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key, Previous: []string{"v-old"}}
}

func TestDeploy(t *testing.T) {
	f := newFixture(t, "fake")
	tgt := f.open("plugins.fake.deploy", map[string]string{"endpoint": "https://cdn.example.com", "mode": "a"}, map[string]string{"token": "s3cret"}).(contracts.Target)
	req := deployRequest(t)
	var lines []contracts.LogLine
	req.Log = func(l contracts.LogLine) { lines = append(lines, l) }
	res, err := tgt.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	serial := req.Chain[0].SerialNumber.Text(16)
	if res.Ref != "v-"+serial || !slices.Equal(res.Removed, []string{"v-old"}) {
		t.Errorf("result %+v", res)
	}
	if len(lines) != 2 || lines[0].Level != contracts.OK || lines[0].Key != i18n.PluginsLogLine || lines[0].Params["text"] != "uploaded" || lines[1].Level != contracts.Info {
		t.Errorf("log %+v", lines)
	}
	sent := f.sent()
	c := sent.Certificate
	if sent.Protocol != protocol || sent.Action != actionDeploy || sent.Type != "deploy" || sent.Config["endpoint"] != "https://cdn.example.com" ||
		sent.Secrets["token"] != "s3cret" || !slices.Equal(sent.Previous, []string{"v-old"}) || c == nil || c.ID != 7 || c.Serial != serial {
		t.Fatalf("sent %+v", sent)
	}
	b, err := cryptox.ParseBundle([]byte(c.Fullchain), []byte(c.Privkey))
	if err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 || !strings.Contains(c.Chain, "BEGIN CERTIFICATE") {
		t.Errorf("the certificate: %v", err)
	}
	if fp := sha256.Sum256(req.Chain[0].Raw); c.Fingerprint != fmt.Sprintf("%x", fp) || !c.NotAfter.Equal(req.Chain[0].NotAfter) {
		t.Errorf("fingerprint %s, not after %v", c.Fingerprint, c.NotAfter)
	}
	msg, err := tgt.(contracts.Tester).Test(t.Context())
	if err != nil || msg.Key != i18n.PluginsTestOk || msg.Params["message"] != "reached https://cdn.example.com" {
		t.Errorf("test %+v %v", msg, err)
	}
	if f.sent().Action != actionTest {
		t.Errorf("the test sent %s", f.sent().Action)
	}
}

func TestFailures(t *testing.T) {
	f := newFixture(t, "fake")
	old := actionTimeout
	t.Cleanup(func() { actionTimeout = old })
	for _, c := range []struct {
		behave    string
		key       i18n.Key
		permanent bool
		delay     time.Duration
		stderr    bool
	}{
		{"fail", i18n.PluginsErrorFailed, true, 0, true},
		{"retry", i18n.PluginsErrorFailed, false, 30 * time.Second, false},
		{"garbage", i18n.PluginsErrorAnswer, true, 0, false},
		{"exit", i18n.PluginsErrorExit, false, 0, true},
		{"slow", i18n.PluginsErrorTimeout, false, 0, false},
		{"protocol2", i18n.PluginsErrorAnswer, true, 0, false},
		{"huge", i18n.PluginsErrorAnswer, true, 0, false},
	} {
		// Only the plugin that hangs needs a short time: starting the test
		// binary may take a second with the race detector.
		actionTimeout = old
		if c.behave == "slow" {
			actionTimeout = 3 * time.Second
		}
		tgt := f.open("plugins.fake.deploy", map[string]string{"endpoint": "https://cdn.example.com", "mode": "a", "behave": c.behave}, nil).(contracts.Target)
		req := deployRequest(t)
		var stderr string
		req.Log = func(l contracts.LogLine) {
			if l.Key == i18n.PluginsLogStderr {
				stderr = l.Detail
			}
		}
		_, err := tgt.Deploy(t.Context(), req)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%s: %v", c.behave, err)
		}
		if d, ok := retry.Delay(err); ok != (c.delay != 0) || d != c.delay {
			t.Errorf("%s: delay %v %v", c.behave, d, ok)
		}
		if (stderr != "") != c.stderr {
			t.Errorf("%s: standard error %q", c.behave, stderr)
		}
	}
}

func TestNotify(t *testing.T) {
	f := newFixture(t, "fake")
	n := f.open("plugins.fake.notes", map[string]string{contracts.NoteLocale: "en-US"}, nil).(contracts.Notifier)
	note := contracts.Note{Event: contracts.CertificateIssueFailed, Title: "example.com failed", Subject: "[JEC] example.com failed", Text: "line", Link: "https://jec.example.com/certificates/3"}
	if err := n.Notify(t.Context(), note); err != nil {
		t.Fatal(err)
	}
	if sent := f.sent(); sent.Action != actionNotify || sent.Type != "notes" || sent.Note == nil || sent.Note.Subject != note.Subject || sent.Note.Link != note.Link {
		t.Errorf("sent %+v", sent)
	}
	if err := n.Close(); err != nil {
		t.Error(err)
	}
}

func TestSolvers(t *testing.T) {
	f := newFixture(t, "fake")
	s := f.open("plugins.fake.dns", nil, nil).(contracts.Solver)
	if timeout, interval := s.Wait(); timeout != 30*time.Second || interval != time.Second {
		t.Errorf("wait %v %v", timeout, interval)
	}
	ch := contracts.Challenge{Domain: "example.com", Token: "tok", KeyAuth: "tok.thumbprint"}
	if err := s.Present(t.Context(), ch); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(ch.KeyAuth))
	if sent := f.sent(); sent.Action != actionDNSPresent || sent.Record == nil || sent.Record.FQDN != "_acme-challenge.example.com." ||
		sent.Record.Value != base64.RawURLEncoding.EncodeToString(sum[:]) || sent.Record.Domain != "example.com" {
		t.Errorf("sent %+v", sent)
	}
	if err := s.CleanUp(t.Context(), ch); err != nil || f.sent().Action != actionDNSCleanUp {
		t.Errorf("clean up: %v", err)
	}

	fs := f.open("plugins.fake.files", nil, nil).(contracts.FileSolver)
	file := contracts.File{Domain: "example.com", Path: "/.well-known/acme-challenge/tok", Content: []byte("tok.thumbprint")}
	if err := fs.PutFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if sent := f.sent(); sent.Action != actionFilePut || sent.File == nil || sent.File.Path != file.Path || sent.File.Content != "tok.thumbprint" {
		t.Errorf("sent %+v", sent)
	}
	if err := fs.RemoveFile(t.Context(), file); err != nil || f.sent().Action != actionFileRemove {
		t.Errorf("remove: %v", err)
	}
	if err := fs.PutFile(t.Context(), contracts.File{Domain: "example.com", Path: "/index.html"}); err == nil || !retry.IsPermanent(err) {
		t.Errorf("a file outside /.well-known/: %v", err)
	}
}

// The plugin runs without JEC's settings, in a directory of its own.
func TestEnvironment(t *testing.T) {
	t.Setenv("JEC_SECRET_KEY", "s3cret")
	env := environ("fake", "/data/plugins/fake.data")
	for _, kv := range env {
		if strings.HasPrefix(kv, "JEC_SECRET_KEY=") {
			t.Error("the plugin gets the master key")
		}
	}
	for _, want := range []string{"JEC_PLUGIN=fake", "JEC_PLUGIN_DATA=/data/plugins/fake.data", "JEC_PLUGIN_PROTOCOL=1"} {
		if !slices.Contains(env, want) {
			t.Errorf("no %s", want)
		}
	}
}

func TestLiteral(t *testing.T) {
	for _, s := range []string{"plain", "it's", "{x}", "{}", "a'{b", "{'", "'{", "}{", "{a}{b}", "x}'y", "''", "#1 <b>", "'{''}'"} {
		m, err := i18n.ParseMessage(literal(s))
		if err != nil {
			t.Errorf("%q: %v", s, err)
			continue
		}
		if got, err := m.Format(i18n.BaseLocale, nil); err != nil || got != s {
			t.Errorf("%q became %q (%s): %v", s, got, literal(s), err)
		}
	}
}

func TestTextIn(t *testing.T) {
	zh, en := language.MustParse("zh-CN"), language.MustParse("en-US")
	for _, c := range []struct {
		t        text
		zh, en   string
		describe string
	}{
		{text{"zh-CN": "cn", "zh-TW": "tw", "en-US": "us"}, "cn", "us", "exact"},
		{text{"zh-TW": "tw", "en": "en"}, "tw", "en", "same language"},
		{text{"": "any", "fr": "fr"}, "any", "any", "for every language"},
		{text{"fr": "fr", "de": "de"}, "de", "de", "the first"},
		{text{"en-GB": "gb", "fr": "fr"}, "gb", "gb", "English"},
	} {
		if got := c.t.in(zh); got != c.zh {
			t.Errorf("%s: zh %q", c.describe, got)
		}
		if got := c.t.in(en); got != c.en {
			t.Errorf("%s: en %q", c.describe, got)
		}
	}
	var tx text
	if err := json.Unmarshal([]byte(`42`), &tx); err == nil {
		t.Error("a number as a text")
	}
}

// No key of the locale files is under plugins.texts, where the texts of
// plugins go.
func TestTextsNamespace(t *testing.T) {
	files, err := i18n.ReadDir(os.DirFS(filepath.Join("..", "..", "..", "i18n", "locales")))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		for key := range f.Messages {
			if strings.HasPrefix(string(key), "plugins.texts.") {
				t.Errorf("%s: %s", f.Tag, key)
			}
		}
	}
}

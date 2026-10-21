package plugins

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
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
  {"id":"dns","kind":"solver","name":"Fake DNS","records":true,"wait":{"timeout":30,"interval":1},"fields":[{"key":"behave","kind":"text"}]},
  {"id":"files","kind":"solver","challenge":"http","name":"Fake files","fields":[{"key":"behave","kind":"text"}]},
  {"id":"ca","kind":"source","challenges":["record","http","record"],"revocable":true,"name":"Fake CA","fields":[{"key":"behave","kind":"text"}]},
  {"id":"store","kind":"source","own_key":true,"holds":true,"name":"Fake store","fields":[{"key":"behave","kind":"text"}]}]}`

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
		fmt.Print(strings.Replace(fakeDescription, `"kind":"notifier"`, `"kind":"feature"`, 1))
		return 0
	case "bad-records":
		fmt.Print(strings.Replace(fakeDescription, `"challenge":"http",`, `"challenge":"http","records":true,`, 1))
		return 0
	case "bad-own-key":
		fmt.Print(strings.Replace(fakeDescription, `"kind":"notifier"`, `"kind":"notifier","own_key":true`, 1))
		return 0
	case "bad-key-challenges":
		fmt.Print(strings.Replace(fakeDescription, `"own_key":true`, `"own_key":true,"challenges":["http"]`, 1))
		return 0
	case "bad-challenges":
		fmt.Print(strings.Replace(fakeDescription, `"challenges":["record","http","record"]`, `"challenges":["dns"]`, 1))
		return 0
	case "bad-revocable":
		fmt.Print(strings.Replace(fakeDescription, `"kind":"notifier"`, `"kind":"notifier","revocable":true`, 1))
		return 0
	case "bad-holds":
		fmt.Print(strings.Replace(fakeDescription, `"own_key":true,"holds":true`, `"holds":true`, 1))
		return 0
	case "wrong-key", "uncovered":
		// A certificate for a key of the CA's own, or for one domain only.
		csr, names := "", req.Issue.Domains
		if behave == "uncovered" {
			csr, names = req.Issue.CSR, names[:1]
		}
		chain, _, err := sign(csr, names)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		return reply(map[string]any{"protocol": 1, "ok": true, "chain": chain})
	case "bad-file", "other-domain", "bad-record":
		w := map[string]any{"after": 60, "state": "ordered"}
		switch behave {
		case "bad-file":
			w["files"] = []file{{Domain: "example.com", Path: "/index.html", Content: "x"}}
		case "other-domain":
			w["records"] = []askedRecord{{Domain: "example.org", Name: "_dnsauth", Type: "TXT", Value: "x"}}
		default:
			w["records"] = []askedRecord{{Domain: "example.com", Name: "@", Type: "MX", Value: "mail.example.com"}}
		}
		return reply(map[string]any{"protocol": 1, "ok": true, "wait": w})
	case "wait-files":
		return reply(map[string]any{"protocol": 1, "ok": true, "wait": map[string]any{"after": 60, "files": []file{{Domain: "example.com", Path: "/.well-known/x", Content: "x"}}}})
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
	case actionDNSAddRecord:
		return reply(map[string]any{"protocol": 1, "ok": true, "ref": "rec-" + req.DNSRecord.Name})
	case actionIssue:
		return issue(req.Issue, reply)
	case actionCollect:
		if req.Collect.Current != "" {
			return reply(map[string]any{"protocol": 1, "ok": true, "wait": map[string]any{"after": 3600, "state": "same"}})
		}
		chain, key, err := sign("", req.Collect.Domains)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		return reply(map[string]any{"protocol": 1, "ok": true, "chain": chain, "key": key})
	case actionHeld:
		return reply(map[string]any{"protocol": 1, "ok": true, "certificates": []heldAnswer{
			{Domains: []string{"example.com", "www.example.com"}, NotAfter: "2099-01-01T08:00:00+08:00"},
			{Domains: []string{"other.example"}, NotAfter: "soon"},
		}})
	}
	return reply(map[string]any{"protocol": 1, "ok": true})
}

// validationFile is the file the CA of the tests asks for.
const validationFile = "/.well-known/pki-validation/fake.txt"

// issue is the CA of the tests: its order asks for a file and a record,
// then for the file only, then it issues.
func issue(o *issueOrder, reply func(any) int) int {
	f := []file{{Domain: "Example.com.", Path: validationFile, Content: "token"}}
	switch o.State {
	case "":
		return reply(map[string]any{"protocol": 1, "ok": true, "wait": map[string]any{
			"after": 5, "state": "ordered", "files": f,
			"records": []askedRecord{{Domain: "www.example.com", Name: "_dnsauth", Type: "cname", Value: "dcv.example.net"}},
		}})
	case "ordered":
		return reply(map[string]any{"protocol": 1, "ok": true, "wait": map[string]any{"after": 100000, "state": "validating", "files": f}})
	}
	chain, _, err := sign(o.CSR, o.Domains)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return reply(map[string]any{"protocol": 1, "ok": true, "chain": chain})
}

// sign issues a certificate of names from a CA made on the spot, for the
// key of csr, in PEM, or else for a key of its own, which it returns too.
// The chain is the leaf, then the intermediate.
func sign(csr string, names []string) (chain, key string, err error) {
	var pub crypto.PublicKey
	if csr != "" {
		block, _ := pem.Decode([]byte(csr))
		if block == nil {
			return "", "", errors.New("no CSR")
		}
		req, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil {
			return "", "", err
		}
		pub = req.PublicKey
	} else {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return "", "", err
		}
		der, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			return "", "", err
		}
		pub, key = k.Public(), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	}
	now := time.Now()
	ca := func(serial int64, name string) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
			IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		}
	}
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	root := ca(1, "Fake Root")
	der, err := x509.CreateCertificate(rand.Reader, root, root, rootKey.Public(), rootKey)
	if err != nil {
		return "", "", err
	}
	if root, err = x509.ParseCertificate(der); err != nil {
		return "", "", err
	}
	inter := ca(2, "Fake Intermediate")
	interDER, err := x509.CreateCertificate(rand.Reader, inter, root, interKey.Public(), rootKey)
	if err != nil {
		return "", "", err
	}
	if inter, err = x509.ParseCertificate(interDER); err != nil {
		return "", "", err
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, inter, pub, interKey)
	if err != nil {
		return "", "", err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: interDER})...)
	return string(out), key, nil
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
	ids := []string{"plugins.fake.deploy", "plugins.fake.notes", "plugins.fake.dns", "plugins.fake.files", "plugins.fake.ca", "plugins.fake.store"}
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
	if ca, _ := f.reg.Type("plugins.fake.ca"); ca.Kind != providers.Source || !ca.NeedsSolver || ca.OwnKey || !ca.Revocable ||
		!slices.Equal(ca.Challenges, []string{providers.ChallengeHTTP, providers.ChallengeRecord}) {
		t.Errorf("ca %+v", ca)
	}
	if store, _ := f.reg.Type("plugins.fake.store"); store.Kind != providers.Source || store.NeedsSolver || !store.OwnKey || store.Revocable || !store.Holds {
		t.Errorf("store %+v", store)
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
		"bad-kind":           i18n.PluginsErrorDescribe,
		"bad-records":        i18n.PluginsErrorDescribe,
		"bad-own-key":        i18n.PluginsErrorDescribe,
		"bad-key-challenges": i18n.PluginsErrorDescribe,
		"bad-challenges":     i18n.PluginsErrorDescribe,
		"bad-revocable":      i18n.PluginsErrorDescribe,
		"bad-holds":          i18n.PluginsErrorDescribe,
		"garbage":            i18n.PluginsErrorAnswer,
		"exit":               i18n.PluginsErrorExit,
		"protocol2":          i18n.PluginsErrorAnswer,
		"fail":               i18n.PluginsErrorFailed,
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

	// Records of any type, for a type that adds them.
	if typ, _ := f.reg.Type("plugins.fake.dns"); !typ.Records {
		t.Error("the type adds no records")
	}
	rs := s.(contracts.RecordSolver)
	rec := contracts.Record{Name: "_dnsauth.example.com", Type: "CNAME", Value: "x.sectigo.com"}
	ref, err := rs.AddRecord(t.Context(), rec)
	if sent := f.sent(); err != nil || ref != "rec-_dnsauth.example.com" || sent.Action != actionDNSAddRecord || sent.DNSRecord == nil ||
		*sent.DNSRecord != (dnsRecord{Name: rec.Name, Type: "CNAME", Value: rec.Value}) {
		t.Errorf("add record: %q %v, sent %+v", ref, err, sent)
	}
	if err := rs.RemoveRecord(t.Context(), rec, ref); err != nil {
		t.Fatal(err)
	}
	if sent := f.sent(); sent.Action != actionDNSRemoveRecord || sent.Ref != ref || sent.DNSRecord == nil || sent.DNSRecord.Name != rec.Name {
		t.Errorf("remove record: sent %+v", sent)
	}
	if _, ok := f.open("plugins.fake.files", nil, nil).(contracts.RecordSolver); ok {
		t.Error("a solver over HTTP adds records")
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

// site is a solver over HTTP of the tests.
type site struct {
	files map[string]string
	puts  int
	fail  error
}

func (s *site) PutFile(_ context.Context, f contracts.File) error {
	if s.fail != nil {
		return s.fail
	}
	s.puts++
	s.files[f.Domain+f.Path] = string(f.Content)
	return nil
}

func (s *site) RemoveFile(_ context.Context, f contracts.File) error {
	delete(s.files, f.Domain+f.Path)
	return nil
}

func (*site) Close() error { return nil }

// zone is a solver of DNS records of the tests.
type zone struct {
	records map[contracts.Record]string // by ref
	fail    error
}

func (z *zone) AddRecord(_ context.Context, r contracts.Record) (string, error) {
	if z.fail != nil {
		return "", z.fail
	}
	z.records[r] = "ref-" + r.Name
	return z.records[r], nil
}

func (z *zone) RemoveRecord(_ context.Context, r contracts.Record, ref string) error {
	if z.records[r] != ref {
		return errors.New("no such record")
	}
	delete(z.records, r)
	return nil
}

func (*zone) Close() error { return nil }

func problemKey(err error) i18n.Key {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p.Key
	}
	return ""
}

func TestSource(t *testing.T) {
	f := newFixture(t, "fake")
	src := f.open("plugins.fake.ca", nil, nil).(contracts.Source)
	if _, ok := src.(contracts.KeySource); ok {
		t.Error("a source that issues for JEC's keys collects")
	}
	old := visible
	t.Cleanup(func() { visible = old })
	visible = func(_ context.Context, name, _, _ string) (bool, error) {
		return name == "_dnsauth.www.example.com", nil
	}
	web, dns := &site{files: map[string]string{}}, &zone{records: map[contracts.Record]string{}}
	key := cryptoxtest.NewKey(t)
	var keys []i18n.Key
	req := contracts.IssueRequest{
		Domains: []string{"example.com", "www.example.com"}, Key: key, Files: web, Records: dns,
		Log: func(l contracts.LogLine) { keys = append(keys, l.Key) },
	}
	attempt := func() *contracts.Wait {
		t.Helper()
		_, err := src.Issue(t.Context(), req)
		var w *contracts.Wait
		if !errors.As(err, &w) {
			t.Fatalf("no wait: %v", err)
		}
		req.State = w.State
		return w
	}

	// The plugin orders with the CSR of the key, and asks for a file and a
	// record, which JEC puts in place.
	if w := attempt(); w.After != time.Minute {
		t.Errorf("wait %v", w.After)
	}
	sent := f.sent()
	block, _ := pem.Decode([]byte(sent.Issue.CSR))
	if block == nil {
		t.Fatalf("sent %+v", sent.Issue)
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || sent.Action != actionIssue || sent.Type != "ca" || !slices.Equal(csr.DNSNames, req.Domains) || csr.Subject.CommonName != "example.com" ||
		!key.PublicKey.Equal(csr.PublicKey) || sent.Issue.State != "" || sent.Issue.Files != nil || sent.Issue.Records != nil {
		t.Fatalf("sent %+v: %v", sent.Issue, err)
	}
	record := contracts.Record{Name: "_dnsauth.www.example.com", Type: "CNAME", Value: "dcv.example.net"}
	if web.files["example.com"+validationFile] != "token" || dns.records[record] != "ref-"+record.Name {
		t.Fatalf("in place: %v %v", web.files, dns.records)
	}

	// The next attempt says what is in place. The plugin then asks for the
	// file only: the record goes, the file stays as it is.
	if w := attempt(); w.After != 24*time.Hour {
		t.Errorf("wait %v", w.After)
	}
	sent = f.sent()
	if sent.Issue.State != "ordered" || !slices.Equal(sent.Issue.Files, []fileInPlace{{Domain: "example.com", Path: validationFile}}) ||
		!slices.Equal(sent.Issue.Records, []recordInPlace{{Name: record.Name, Type: "CNAME", Value: record.Value, Visible: true}}) {
		t.Fatalf("sent %+v", sent.Issue)
	}
	if len(dns.records) != 0 || web.puts != 1 {
		t.Errorf("records %v, %d puts", dns.records, web.puts)
	}

	// Issued: the chain for the key, and what JEC put in place goes.
	chain, err := src.Issue(t.Context(), req)
	if err != nil || len(chain) != 2 || !key.PublicKey.Equal(chain[0].PublicKey) || chain[1].Subject.CommonName != "Fake Intermediate" {
		t.Fatalf("issued %v: %v", chain, err)
	}
	if f.sent().Issue.State != "validating" || len(web.files) != 0 {
		t.Errorf("state %q, files %v", f.sent().Issue.State, web.files)
	}
	if want := []i18n.Key{i18n.PluginsLogPutFile, i18n.PluginsLogAddRecord, i18n.PluginsLogRemoveRecord, i18n.PluginsLogRemoveFile}; !slices.Equal(keys, want) {
		t.Errorf("log %v", keys)
	}
}

func TestSourceFailures(t *testing.T) {
	f := newFixture(t, "fake")
	key := cryptoxtest.NewKey(t)
	web, dns := &site{files: map[string]string{}}, &zone{records: map[contracts.Record]string{}}
	issue := func(behave string, req contracts.IssueRequest) error {
		t.Helper()
		src := f.open("plugins.fake.ca", map[string]string{"behave": behave}, nil).(contracts.Source)
		req.Domains, req.Key = []string{"example.com", "www.example.com"}, key
		_, err := src.Issue(t.Context(), req)
		return err
	}
	for behave, k := range map[string]i18n.Key{
		"wrong-key": i18n.PluginsErrorChain, "uncovered": i18n.PluginsErrorChain,
		"bad-file": i18n.PluginsErrorValidation, "other-domain": i18n.PluginsErrorValidation, "bad-record": i18n.PluginsErrorValidation,
	} {
		if err := issue(behave, contracts.IssueRequest{Files: web, Records: dns}); problemKey(err) != k || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", behave, err)
		}
	}

	// The certificate's solver cannot do what the plugin asks for. What
	// got in place, and the plugin's state, stay for the next attempt.
	err := issue("", contracts.IssueRequest{Files: web})
	var is issuance
	if problemKey(err) != i18n.PluginsErrorNoRecords || !retry.IsPermanent(err) || json.Unmarshal(contracts.KeptState(err), &is) != nil ||
		is.State != "ordered" || len(is.Files) != 1 {
		t.Errorf("no records: %v, kept %s", err, contracts.KeptState(err))
	}
	if err := issue("", contracts.IssueRequest{Records: dns}); problemKey(err) != i18n.PluginsErrorNoFiles || !retry.IsPermanent(err) {
		t.Errorf("no files: %v", err)
	}

	// The solver fails: why, if it says, or else that it did.
	dns.fail = errors.New("quota")
	err = issue("", contracts.IssueRequest{Files: web, Records: dns})
	if problemKey(err) != i18n.PluginsErrorAddRecord || retry.IsPermanent(err) {
		t.Errorf("a refused record: %v", err)
	}
	// The next attempt goes on: the plugin is past its order, and the file
	// is in place already.
	dns.fail, web.puts = nil, 0
	var w *contracts.Wait
	if err := issue("", contracts.IssueRequest{Files: web, Records: dns, State: contracts.KeptState(err)}); !errors.As(err, &w) || web.puts != 0 || f.sent().Issue.State != "ordered" {
		t.Errorf("the attempt after: %v, %d puts", err, web.puts)
	}
	web.fail = retry.Permanent(&contracts.Problem{Key: "test.denied"})
	if err := issue("", contracts.IssueRequest{Files: web, Records: dns}); problemKey(err) != "test.denied" || !retry.IsPermanent(err) {
		t.Errorf("a file refused with a reason: %v", err)
	}
	web.fail = errors.New("disk full")
	if err := issue("", contracts.IssueRequest{Files: web, Records: dns}); problemKey(err) != i18n.PluginsErrorPutFile || retry.IsPermanent(err) {
		t.Errorf("a refused file: %v", err)
	}
}

func TestKeySource(t *testing.T) {
	f := newFixture(t, "fake")
	ks := f.open("plugins.fake.store", nil, nil).(contracts.KeySource)
	domains := []string{"example.com", "www.example.com"}
	got, err := ks.Collect(t.Context(), contracts.CollectRequest{Domains: domains})
	if err != nil || len(got.Chain) != 2 || !cryptox.Covers(cryptox.Names(got.Chain[0]), domains) {
		t.Fatalf("collected %+v: %v", got, err)
	}
	if _, err := cryptox.NewBundle(got.Chain, got.Key); err != nil {
		t.Errorf("the key: %v", err)
	}

	// Nothing newer than the certificate in use: the plugin waits.
	end := time.Date(2026, 12, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	_, err = ks.Collect(t.Context(), contracts.CollectRequest{Domains: domains, Current: "abc", CurrentEnd: end, State: []byte("before")})
	var w *contracts.Wait
	sent := f.sent()
	if !errors.As(err, &w) || w.After != time.Hour || string(w.State) != "same" || sent.Action != actionCollect || sent.Collect.Current != "abc" ||
		sent.Collect.CurrentEnd != "2026-11-30T16:00:00Z" || sent.Collect.State != "before" {
		t.Errorf("wait %+v %v, sent %+v", w, err, sent.Collect)
	}

	// What it holds, to pick from.
	held, err := ks.(contracts.Holder).Held(t.Context())
	if err != nil || len(held) != 2 || len(held[0].Domains) != 2 || !held[0].NotAfter.Equal(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)) || !held[1].NotAfter.IsZero() || f.sent().Action != actionHeld {
		t.Errorf("held %+v: %v", held, err)
	}

	ks = f.open("plugins.fake.store", map[string]string{"behave": "wait-files"}, nil).(contracts.KeySource)
	if _, err := ks.Collect(t.Context(), contracts.CollectRequest{Domains: domains}); problemKey(err) != i18n.PluginsErrorValidation || !retry.IsPermanent(err) {
		t.Errorf("files for a source with keys of its own: %v", err)
	}
}

func TestRevoke(t *testing.T) {
	f := newFixture(t, "fake")
	ca := cryptoxtest.NewCA(t)
	leaf := ca.Leaf(t, cryptoxtest.NewKey(t), time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com")
	src := f.open("plugins.fake.ca", nil, nil).(contracts.Revoker)
	if err := src.Revoke(t.Context(), leaf, contracts.ReasonKeyCompromise); err != nil {
		t.Fatal(err)
	}
	sent := f.sent()
	fp := sha256.Sum256(leaf.Raw)
	if sent.Action != actionRevoke || sent.Type != "ca" || sent.Revoke == nil || sent.Revoke.Reason != "key_compromise" ||
		sent.Revoke.Serial != leaf.SerialNumber.Text(16) || sent.Revoke.Fingerprint != hex.EncodeToString(fp[:]) {
		t.Fatalf("sent %+v", sent.Revoke)
	}
	if got, err := cryptox.ParseCertificates([]byte(sent.Revoke.Certificate)); err != nil || len(got) != 1 || !got[0].Equal(leaf) {
		t.Errorf("the certificate sent: %v, %v", got, err)
	}
	src = f.open("plugins.fake.ca", map[string]string{"behave": "fail"}, nil).(contracts.Revoker)
	if err := src.Revoke(t.Context(), leaf, contracts.ReasonSuperseded); problemKey(err) != i18n.PluginsErrorFailed {
		t.Errorf("refused: %v", err)
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

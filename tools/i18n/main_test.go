package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

func TestConstName(t *testing.T) {
	tests := map[i18n.Key]string{
		"certs.list.empty_state": "CertsListEmptyState",
		"a.b2.c_d":               "AB2CD",
		"kernel.error.not_found": "KernelErrorNotFound",
	}
	for k, want := range tests {
		if got := constName(k); got != want {
			t.Errorf("constName(%s) = %s, want %s", k, got, want)
		}
	}
}

func TestValidKey(t *testing.T) {
	for _, k := range []i18n.Key{"certs.list.empty", "provider.aliyun_esa.field.site_id.label", "a.b2.c"} {
		if err := validKey(k); err != nil {
			t.Errorf("validKey(%s) = %v", k, err)
		}
	}
	for _, k := range []i18n.Key{"certs.list", "Certs.list.x", "certs..x", "certs.list.x-y", "certs.1list.x", "certs.list."} {
		if err := validKey(k); err == nil {
			t.Errorf("validKey(%s) passed", k)
		}
	}
}

func TestKeysSourceCollision(t *testing.T) {
	base := &i18n.File{Tag: i18n.BaseLocale, Messages: map[i18n.Key]string{"a.b.c_d": "x", "a.b_c.d": "y"}}
	if _, err := keysSource(base); err == nil || !strings.Contains(err.Error(), "ABCD") {
		t.Errorf("keysSource with colliding names = %v", err)
	}
}

func TestCompleteness(t *testing.T) {
	base := &i18n.File{Messages: map[i18n.Key]string{"a.b.c": "1", "a.b.d": "2", "a.b.e": "3"}}
	part := &i18n.File{Messages: map[i18n.Key]string{"a.b.c": "1", "x.y.z": "extra"}}
	if got := completeness(part, base); got != 33 {
		t.Errorf("completeness = %d, want 33", got)
	}
	if got := completeness(base, base); got != 100 {
		t.Errorf("completeness(base) = %d", got)
	}
	if got := completeness(part, &i18n.File{}); got != 100 {
		t.Errorf("completeness against an empty base = %d", got)
	}
}

// CJK text for the scanner tests, built at run time so that this file
// itself stays free of it.
var (
	cjk      = string([]rune{0x4e2d, 0x6587})
	cjkPunct = string(rune(0x3002))
)

var (
	baseMessages = map[i18n.Key]string{
		"certs.list.title": "Certificates for {owner}",
		"certs.list.count": "{n, plural, one {# certificate} other {# certificates}}",
		"certs.kind.label": "{kind, select, dns {DNS} other {Other}}",
	}
	zhMessages = map[i18n.Key]string{
		"certs.list.title": "zh {owner}",
		"certs.list.count": "{n, plural, other {# zh}}",
		"certs.kind.label": "{kind, select, dns {zh-dns} other {zh-other}}",
	}
	usingCode = `package x

import "github.com/XMZO/JustEnoughCerts/i18n"

var keys = []i18n.Key{i18n.CertsListTitle, i18n.CertsListCount, i18n.CertsKindLabel}
`
)

// repo writes a minimal repository with the two required locales, the
// generated keys and code that uses every key; change lets a test alter the
// messages first, and extra adds or replaces files.
func repo(t *testing.T, change func(en, zh map[i18n.Key]string), extra map[string]string) string {
	t.Helper()
	root := t.TempDir()
	en := clone(baseMessages)
	zh := clone(zhMessages)
	if change != nil {
		change(en, zh)
	}
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	baseFile := &i18n.File{Tag: i18n.BaseLocale, Meta: i18n.Meta{Name: "English", Dir: "ltr"}, Messages: en}
	zhFile := &i18n.File{Tag: requiredLocales[1], Meta: i18n.Meta{Name: "Chinese", Dir: "ltr"}, Messages: zh}
	for _, f := range []*i18n.File{baseFile, zhFile} {
		data, err := canonical(f, baseFile)
		if err != nil {
			t.Fatal(err)
		}
		write("i18n/locales/"+f.Tag.String()+".json", string(data))
	}
	keys, err := keysSource(baseFile)
	if err != nil {
		t.Fatal(err)
	}
	write("i18n/keys_gen.go", string(keys))
	write("web/src/i18n/generated.ts", string(tsSource([]*i18n.File{zhFile, baseFile}, baseFile)))
	write("internal/x/x.go", usingCode)
	for rel, content := range extra {
		write(rel, content)
	}
	return root
}

func clone(m map[i18n.Key]string) map[i18n.Key]string {
	out := make(map[i18n.Key]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestChecksPass(t *testing.T) {
	root := repo(t, nil, map[string]string{
		"docs/notes.md":                cjk + " docs are fine",
		"README.md":                    cjk + " readme is fine",
		"internal/x/testdata/zh.txt":   cjk + " fixtures are fine",
		"internal/y/aliased.go":        "package y\n\nimport t \"github.com/XMZO/JustEnoughCerts/i18n\"\n\nvar _ = t.CertsListTitle\n",
		"internal/x/node_modules/x.js": cjk,
	})
	if p := runChecks(root); len(p) > 0 {
		t.Errorf("problems in a clean repository:\n%s", strings.Join(p, "\n"))
	}
}

func TestChecksFail(t *testing.T) {
	tests := []struct {
		name   string
		change func(en, zh map[i18n.Key]string)
		extra  map[string]string
		want   string
	}{
		{"missing translation", func(_, zh map[i18n.Key]string) { delete(zh, "certs.list.title") }, nil, "zh-CN.json: certs.list.title is missing"},
		{"extra key", func(_, zh map[i18n.Key]string) { zh["certs.list.extra"] = "x" }, nil, "certs.list.extra is not in en-US.json"},
		{"argument mismatch", func(_, zh map[i18n.Key]string) { zh["certs.list.title"] = "zh {user}" }, nil, "certs.list.title: arguments"},
		{"plural as text", func(_, zh map[i18n.Key]string) { zh["certs.list.count"] = "{n} zh" }, nil, "certs.list.count: arguments"},
		{"plural categories", func(_, zh map[i18n.Key]string) { zh["certs.list.count"] = "{n, plural, one {#} other {#}}" }, nil, "has categories [one other], zh-CN uses [other]"},
		{"base plural categories", func(en, _ map[i18n.Key]string) { en["certs.list.count"] = "{n, plural, other {#}}" }, nil, "en-US.json: certs.list.count: plural n"},
		{"select branches", func(_, zh map[i18n.Key]string) { zh["certs.kind.label"] = "{kind, select, http {x} other {y}}" }, nil, "select branches"},
		{"syntax error", func(_, zh map[i18n.Key]string) { zh["certs.list.title"] = "{owner" }, nil, "zh-CN.json: certs.list.title: at character"},
		{"empty message", func(_, zh map[i18n.Key]string) { zh["certs.list.title"] = "  " }, nil, "certs.list.title is empty"},
		{"bad key name", func(en, zh map[i18n.Key]string) { en["certs.title"] = "x"; zh["certs.title"] = "x" }, nil, "certs.title: a key needs at least three segments"},
		{"unused key", nil, map[string]string{"internal/x/x.go": "package x\n"}, "certs.kind.label is not used by any code"},
		{"stale web keys", nil, map[string]string{"web/src/i18n/generated.ts": "export {}\n"}, "generated.ts is stale"},
		{"key used by tests only", nil, map[string]string{
			"internal/x/x.go":      "package x\n",
			"internal/x/x_test.go": usingCode,
		}, "certs.kind.label is not used by any code"},
		{"stale keys", nil, map[string]string{"i18n/keys_gen.go": "package i18n\n"}, "keys_gen.go is stale"},
		{"not canonical", nil, map[string]string{"i18n/locales/zh-CN.json": `{"_meta": {"name": "Chinese", "dir": "ltr", "completeness": 100}, "certs": {"kind": {"label": "{kind, select, dns {a} other {b}}"}, "list": {"count": "{n, plural, other {#}}", "title": "{owner}"}}}`}, "zh-CN.json is not in canonical form"},
		{"CJK in code", nil, map[string]string{"internal/x/msg.go": "package x\n\nconst hello = \"" + cjk + "\"\n"}, "internal/x/msg.go:3: CJK text outside i18n/locales"},
		{"CJK in a script", nil, map[string]string{"scripts/run.sh": "echo " + cjkPunct + "\n"}, "scripts/run.sh:1: CJK text"},
		{"missing zh-CN", nil, map[string]string{"i18n/locales/zh-CN.json": ""}, "zh-CN.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := runChecks(repo(t, tt.change, tt.extra))
			for _, msg := range p {
				if strings.Contains(msg, tt.want) {
					return
				}
			}
			t.Errorf("no problem contains %q; got:\n%s", tt.want, strings.Join(p, "\n"))
		})
	}
}

func TestWebKeysCount(t *testing.T) {
	// Go uses two of the keys, the web UI the third one.
	root := repo(t, nil, map[string]string{
		"internal/x/x.go":        "package x\n\nimport \"github.com/XMZO/JustEnoughCerts/i18n\"\n\nvar _ = []i18n.Key{i18n.CertsListTitle, i18n.CertsListCount}\n",
		"web/src/pages/Kind.vue": "<template><p>{{ t('certs.kind.label', { kind }) }}</p></template>\n",
	})
	if p := runChecks(root); len(p) > 0 {
		t.Errorf("a key used by the web UI counts as unused:\n%s", strings.Join(p, "\n"))
	}
}

func TestTSSource(t *testing.T) {
	base := &i18n.File{Tag: i18n.BaseLocale, Meta: i18n.Meta{Name: "English", Dir: "ltr"}, Messages: map[i18n.Key]string{"a.b.c": "x", "a.b.a": "y"}}
	zh := &i18n.File{Tag: requiredLocales[1], Meta: i18n.Meta{Name: cjk, Dir: "ltr"}, Messages: map[i18n.Key]string{"a.b.c": "x"}}
	got := string(tsSource([]*i18n.File{zh, base}, base))
	for _, want := range []string{
		"{ tag: \"en-US\", name: \"English\", dir: \"ltr\", completeness: 100 },\n  { tag: \"zh-CN\", name: \"\\u4e2d\\u6587\", dir: \"ltr\", completeness: 50 },",
		"export type MessageKey =\n  | \"a.b.a\"\n  | \"a.b.c\"\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tsSource lacks %q:\n%s", want, got)
		}
	}
	if strings.ContainsFunc(got, isCJK) {
		t.Error("tsSource emits CJK text")
	}
}

func TestFixAndGen(t *testing.T) {
	root := repo(t, nil, map[string]string{
		"i18n/locales/zh-CN.json": `{"certs": {"list": {"title": "zh {owner}"}}, "_meta": {"name": "Chinese", "dir": "ltr", "completeness": 7}}`,
		"i18n/keys_gen.go":        "package i18n\n",
	})
	if err := fix(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "i18n", "locales", "zh-CN.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "{\n  \"_meta\": {\n    \"name\": \"Chinese\",\n    \"dir\": \"ltr\",\n    \"completeness\": 33\n  },") {
		t.Errorf("fixed zh-CN.json:\n%s", data)
	}
	// Only the missing translations remain as problems.
	for _, msg := range runChecks(root) {
		if !strings.Contains(msg, "is missing") {
			t.Errorf("unexpected problem after fix: %s", msg)
		}
	}
}

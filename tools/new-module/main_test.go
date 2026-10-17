package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

// repo writes the parts of a repository that scaffold touches.
func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "i18n", "locales")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for tag, name := range map[string]string{"en-US": "English", "zh-CN": "Chinese"} {
		f := &i18n.File{Meta: i18n.Meta{Name: name, Dir: "ltr", Completeness: 100}, Messages: map[i18n.Key]string{"kernel.app.name": "JEC"}}
		data, err := f.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, tag+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestScaffold(t *testing.T) {
	root := repo(t)
	if err := scaffold(root, "dns_pod", "solver"); err != nil {
		t.Fatal(err)
	}

	want := map[string][]string{
		"internal/modules/dns_pod/module.go": {
			"package dnspod",
			`ID:          "dns_pod"`,
			"module.Solver",
			"i18n.DnsPodModuleName",
			"i18n.DnsPodModuleDescription",
		},
		"internal/modules/dns_pod/module_test.go": {"package dnspod", "module.NewSet(New())"},
		"internal/modules/all/dns_pod.go": {
			"//go:build !no_dns_pod",
			`import dnspod "github.com/XMZO/JustEnoughCerts/internal/modules/dns_pod"`,
			"dnspod.New()",
		},
	}
	fset := token.NewFileSet()
	for rel, parts := range want {
		path := filepath.Join(root, filepath.FromSlash(rel))
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parser.ParseFile(fset, path, src, parser.ParseComments); err != nil {
			t.Errorf("%s does not parse: %v", rel, err)
		}
		for _, part := range parts {
			if !strings.Contains(string(src), part) {
				t.Errorf("%s does not contain %q:\n%s", rel, part, src)
			}
		}
	}

	files, err := i18n.ReadDir(os.DirFS(filepath.Join(root, "i18n", "locales")))
	if err != nil || len(files) != 2 {
		t.Fatalf("locale files: %v, %v", files, err)
	}
	for _, f := range files {
		for _, key := range []i18n.Key{"dns_pod.module.name", "dns_pod.module.description"} {
			if !strings.HasPrefix(f.Messages[key], "TODO:") {
				t.Errorf("%s: %s = %q, want a placeholder", f.Tag, key, f.Messages[key])
			}
		}
		if f.Messages["kernel.app.name"] != "JEC" {
			t.Errorf("%s lost its messages: %v", f.Tag, f.Messages)
		}
	}

	if err := scaffold(root, "dns_pod", "solver"); err == nil || !strings.Contains(err.Error(), "exists already") {
		t.Errorf("scaffolding dns_pod again: %v", err)
	}
}

func TestScaffoldPlainID(t *testing.T) {
	root := repo(t)
	if err := scaffold(root, "acme", "source"); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(root, "internal", "modules", "all", "acme.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "import \"github.com/XMZO/JustEnoughCerts/internal/modules/acme\"\n") {
		t.Errorf("acme.go imports with a needless name:\n%s", src)
	}
}

func TestScaffoldRejects(t *testing.T) {
	for _, tt := range []struct{ id, category string }{
		{"", "source"},
		{"Acme", "source"},
		{"1acme", "source"},
		{"acme-dns", "source"},
		{strings.Repeat("a", 33), "source"},
		{"all", "feature"},
		{"kernel", "feature"},
		{"acme", "other"},
	} {
		if err := scaffold(repo(t), tt.id, tt.category); err == nil {
			t.Errorf("scaffold(%q, %q) succeeded", tt.id, tt.category)
		}
	}
}

func TestCamel(t *testing.T) {
	for in, want := range map[string]string{"acme": "Acme", "dns_pod": "DnsPod", "a_b_c": "ABC", "x2": "X2"} {
		if got := camel(in); got != want {
			t.Errorf("camel(%q) = %q, want %q", in, got, want)
		}
	}
}

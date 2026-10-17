// Command new-module scaffolds a module (PLAN.md 4.2): its package under
// internal/modules, a test, its registration in package all and the two
// messages every module needs, whose placeholder texts the i18n check
// refuses until they are written.
//
//	go run ./tools/new-module -id acme -category source
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

var (
	idPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	categories = map[string]string{
		"source": "Source", "solver": "Solver", "target": "Target", "notifier": "Notifier", "feature": "Feature",
	}
)

func main() {
	root := flag.String("root", ".", "repository root")
	id := flag.String("id", "", "module ID: lowercase letters, digits and underscores, starting with a letter")
	category := flag.String("category", "feature", "source, solver, target, notifier or feature")
	fix := flag.Bool("fix", true, "run go run ./tools/i18n fix afterwards")
	flag.Parse()
	if err := scaffold(*root, *id, *category); err != nil {
		fmt.Fprintln(os.Stderr, "new-module:", err)
		os.Exit(1)
	}
	if *fix {
		cmd := exec.Command("go", "run", "./tools/i18n", "fix")
		cmd.Dir, cmd.Stdout, cmd.Stderr = *root, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "new-module: go run ./tools/i18n fix:", err)
			os.Exit(1)
		}
	}
	fmt.Printf(`Created module %[1]s. Next:
  - write its name and description in i18n/locales/*.json (keys %[1]s.module.*)
  - say what it does in internal/modules/%[1]s/module.go
  - add migrations, API operations and their tests
`, *id)
}

type data struct {
	ID, Package, Category, Const string
}

func scaffold(root, id, category string) error {
	if !idPattern.MatchString(id) || slices.Contains([]string{"all", "kernel"}, id) {
		return fmt.Errorf("module ID %q: want 1 to 32 lowercase letters, digits and underscores, starting with a letter, and not all or kernel", id)
	}
	cat, ok := categories[category]
	if !ok {
		return fmt.Errorf("unknown category %q", category)
	}
	dir := filepath.Join(root, "internal", "modules", id)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s exists already", dir)
	}
	d := data{ID: id, Package: strings.ReplaceAll(id, "_", ""), Category: cat, Const: camel(id)}

	files := map[string]string{
		filepath.Join(dir, "module.go"):                             moduleTemplate,
		filepath.Join(dir, "module_test.go"):                        testTemplate,
		filepath.Join(root, "internal", "modules", "all", id+".go"): registerTemplate,
	}
	for path, text := range files {
		if err := render(path, text, d); err != nil {
			return err
		}
	}
	return addMessages(filepath.Join(root, "i18n", "locales"), id)
}

// camel turns "dns_pod" into "DnsPod", as tools/i18n names key constants.
func camel(s string) string {
	var b strings.Builder
	for _, w := range strings.Split(s, "_") {
		if w != "" {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}

func render(path, text string, d data) error {
	var b bytes.Buffer
	if err := template.Must(template.New(path).Parse(text)).Execute(&b, d); err != nil {
		return err
	}
	src, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: a source directory
		return err
	}
	return os.WriteFile(path, src, 0o644) //nolint:gosec // G306: a source file
}

// addMessages gives every locale file the module's name and description,
// as placeholders: the i18n check refuses texts that start with "TODO:".
func addMessages(dir, id string) error {
	files, err := i18n.ReadDir(os.DirFS(dir))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no locale files in " + dir)
	}
	for _, f := range files {
		for key, text := range map[i18n.Key]string{
			i18n.Key(id + ".module.name"):        "TODO: the name of the " + id + " module",
			i18n.Key(id + ".module.description"): "TODO: what the " + id + " module does, in one line",
		} {
			if _, exists := f.Messages[key]; !exists {
				f.Messages[key] = text
			}
		}
		data, err := f.Encode()
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, f.Tag.String()+".json"), data, 0o644); err != nil { //nolint:gosec // G306: a source file
			return err
		}
	}
	return nil
}

const moduleTemplate = `// Package {{.Package}} is the {{.ID}} module.
//
// TODO: say what the module does.
package {{.Package}}

import (
	"io/fs"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Module is the {{.ID}} module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "{{.ID}}",
		Category:    module.{{.Category}},
		Name:        i18n.{{.Const}}ModuleName,
		Description: i18n.{{.Const}}ModuleDescription,
	}
}

// Migrations returns the module's *.sql migrations, nil while it has none.
// Its tables start with "{{.ID}}_"; to add some, put them in migrations/
// and return them from an embed.FS.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the module's API operations, under /api/v1/{{.ID}}.
func (*Module) Register(*module.Registry) error { return nil }
`

const testTemplate = `package {{.Package}}

import (
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

func TestManifest(t *testing.T) {
	if _, err := module.NewSet(New()); err != nil {
		t.Fatal(err)
	}
}
`

const registerTemplate = `//go:build !no_{{.ID}}

package all

import {{if ne .Package .ID}}{{.Package}} {{end}}"github.com/XMZO/JustEnoughCerts/internal/modules/{{.ID}}"

func init() { modules = append(modules, {{.Package}}.New()) }
`

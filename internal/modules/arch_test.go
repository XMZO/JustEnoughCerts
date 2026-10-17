package modules

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	repoPkg    = "github.com/XMZO/JustEnoughCerts/"
	modulesPkg = repoPkg + "internal/modules/"
)

// imports maps each Go file under dir, as a slash path relative to the
// repository, to its import paths.
func imports(t *testing.T, root, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			out[filepath.ToSlash(rel)] = append(out[filepath.ToSlash(rel)], p)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

// owner returns the module a path below internal/modules/ belongs to.
func owner(path string) string {
	rest, _ := strings.CutPrefix(path, "internal/modules/")
	id, _, _ := strings.Cut(rest, "/")
	return id
}

func TestModulesStandAlone(t *testing.T) {
	root := filepath.Join("..", "..")
	for file, imps := range imports(t, root, "internal/modules") {
		from := owner(file)
		if from == "all" || !strings.Contains(strings.TrimPrefix(file, "internal/modules/"), "/") {
			continue // the list of every module, and this package
		}
		for _, imp := range imps {
			to, ok := strings.CutPrefix(imp, modulesPkg)
			if !ok {
				if strings.HasPrefix(imp, repoPkg+"cmd/") || strings.HasPrefix(imp, repoPkg+"tools/") {
					t.Errorf("%s imports %s", file, imp)
				}
				continue
			}
			if id, _, _ := strings.Cut(to, "/"); id != from {
				t.Errorf("%s imports %s: modules only talk through the kernel", file, imp)
			}
		}
	}
}

func TestKernelImportsNoModule(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, dir := range []string{"internal/kernel", "i18n", "web"} {
		for file, imps := range imports(t, root, dir) {
			for _, imp := range imps {
				if strings.HasPrefix(imp, modulesPkg) {
					t.Errorf("%s imports %s: the kernel must not depend on a module", file, imp)
				}
			}
		}
	}
}

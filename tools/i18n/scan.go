package main

import (
	"bufio"
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

const i18nPkg = "github.com/XMZO/JustEnoughCerts/i18n"

// skipDir reports directories no gate looks into: VCS data, build output,
// dependencies, local data and fixtures.
func skipDir(rel string, d fs.DirEntry) bool {
	switch rel {
	case ".git", ".cache", "build", "dist", "data", "deploy/data", "web/dist":
		return true
	}
	return d.Name() == "node_modules" || d.Name() == "testdata"
}

// walk calls fn for every regular file under root that the gates cover,
// with its slash-separated path relative to root.
func walk(root string, fn func(rel, path string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && skipDir(rel, d) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return fn(rel, path)
	})
}

// usedKeyConstants returns the names of the i18n key constants that Go code
// refers to, outside the generated file and tests: a key only tests use is
// still dead.
func usedKeyConstants(root string) (map[string]bool, error) {
	used := map[string]bool{}
	fset := token.NewFileSet()
	err := walk(root, func(rel, path string) error {
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || rel == "i18n/keys_gen.go" {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		// Inside package i18n the constants are plain identifiers.
		if strings.HasPrefix(rel, "i18n/") && !strings.Contains(strings.TrimPrefix(rel, "i18n/"), "/") {
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					used[id.Name] = true
				}
				return true
			})
			return nil
		}
		local := ""
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == i18nPkg {
				local = "i18n"
				if imp.Name != nil {
					local = imp.Name.Name
				}
			}
		}
		if local == "" || local == "_" {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if x, ok := n.X.(*ast.Ident); ok && x.Name == local {
					used[n.Sel.Name] = true
				}
			case *ast.Ident:
				if local == "." {
					used[n.Name] = true
				}
			}
			return true
		})
		return nil
	})
	return used, err
}

// webKeyPattern matches a quoted message key in TypeScript or a Vue
// template; keys are typed, so a key the UI uses appears as a literal.
var webKeyPattern = regexp.MustCompile("[\"'`]([a-z][a-z0-9_]*(?:\\.[a-z][a-z0-9_]*){2,})[\"'`]")

// usedWebKeys returns the message keys that the web UI's sources quote,
// leaving out the generated list of all keys.
func usedWebKeys(root string) (map[i18n.Key]bool, error) {
	used := map[i18n.Key]bool{}
	src := filepath.Join(root, "web", "src")
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return used, nil
	}
	err := walk(src, func(rel, path string) error {
		source := strings.HasSuffix(rel, ".ts") || strings.HasSuffix(rel, ".vue")
		if !source || rel == "i18n/generated.ts" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range webKeyPattern.FindAllSubmatch(data, -1) {
			used[i18n.Key(m[1])] = true
		}
		return nil
	})
	return used, err
}

// isCJK reports Chinese, Japanese and Korean script and punctuation.
func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
		r >= 0x3000 && r <= 0x303f || // CJK symbols and punctuation
		r >= 0xff00 && r <= 0xffef // full-width forms
}

// scanCJK reports CJK text outside the locale files, the docs and test
// fixtures: user-facing text belongs in the locale files.
func scanCJK(root string, p *problems) error {
	return walk(root, func(rel, path string) error {
		if rel == "i18n/locales" || strings.HasPrefix(rel, "i18n/locales/") ||
			strings.HasPrefix(rel, "docs/") || strings.HasSuffix(rel, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return nil // binary
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for line := 1; sc.Scan(); line++ {
			if strings.ContainsFunc(sc.Text(), isCJK) {
				p.add("%s:%d: CJK text outside i18n/locales: move it to the locale files", rel, line)
			}
		}
		return sc.Err()
	})
}

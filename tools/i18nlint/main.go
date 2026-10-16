// Command i18nlint reports i18n keys that are not the generated constants:
// a string literal, an untyped string constant or a conversion used as an
// i18n.Key. Every user-facing text must come from the locale files, so a
// key the code makes up would bypass the i18n gates.
//
// Tests are not checked, nor the packages that handle keys as data: the
// i18n package, which holds the generated constants, and its tooling.
//
//	go run ./tools/i18nlint ./...
package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/analysis/singlechecker"
	"golang.org/x/tools/go/ast/inspector"
)

const i18nPkg = "github.com/XMZO/JustEnoughCerts/i18n"

var exempt = map[string]bool{
	i18nPkg: true,
	"github.com/XMZO/JustEnoughCerts/tools/i18n": true,
}

// Analyzer reports i18n.Key values that do not come from generated constants.
var Analyzer = &analysis.Analyzer{
	Name:     "i18nkey",
	Doc:      "report i18n.Key values written as literals or conversions instead of generated constants",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func main() { singlechecker.Main(Analyzer) }

func isKey(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Name() == "Key" && obj.Pkg() != nil && obj.Pkg().Path() == i18nPkg
}

func run(pass *analysis.Pass) (any, error) {
	if exempt[pass.Pkg.Path()] {
		return nil, nil
	}
	insp, _ := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	// The operand of a reported conversion is not reported again. Preorder
	// visits a conversion before its operand.
	reported := map[ast.Expr]bool{}
	nodes := []ast.Node{(*ast.BasicLit)(nil), (*ast.Ident)(nil), (*ast.CallExpr)(nil)}
	insp.Preorder(nodes, func(n ast.Node) {
		if strings.HasSuffix(pass.Fset.File(n.Pos()).Name(), "_test.go") {
			return
		}
		switch n := n.(type) {
		case *ast.BasicLit:
			if n.Kind == token.STRING && !reported[n] && isKey(pass.TypesInfo.TypeOf(n)) {
				pass.Reportf(n.Pos(), "string literal used as an i18n.Key: use a generated constant")
			}
		case *ast.Ident:
			c, ok := pass.TypesInfo.Uses[n].(*types.Const)
			if !ok || reported[n] || !isKey(pass.TypesInfo.TypeOf(n)) {
				return
			}
			if b, ok := c.Type().(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
				pass.Reportf(n.Pos(), "untyped constant %s used as an i18n.Key: use a generated constant", n.Name)
			}
		case *ast.CallExpr:
			if tv, ok := pass.TypesInfo.Types[n.Fun]; ok && tv.IsType() && isKey(tv.Type) && len(n.Args) == 1 {
				pass.Reportf(n.Pos(), "conversion to i18n.Key: use a generated constant")
				reported[ast.Unparen(n.Args[0])] = true
			}
		}
	})
	return nil, nil
}

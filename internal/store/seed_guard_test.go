package store_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const storeImport = "github.com/Busnes-app/kycalendar/internal/store"

// store.Seed issues app passwords with no session check. It is for fixtures only: production
// code must never reach it, or the issuance serialization means nothing.
func TestSeedGrantorIsTestOnly(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(root, "internal", "store")
	fset := token.NewFileSet()
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		checked++
		inStore := filepath.Dir(path) == storeDir
		names := map[string]bool{} // every name this file imports the store package under
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == storeImport {
				name := "store"
				if imp.Name != nil {
					name = imp.Name.Name
				}
				if name == "." { // Seed would read as a bare identifier: refuse the import itself
					t.Errorf("%s: dot import of the store package", fset.Position(imp.Pos()))
				}
				names[name] = true
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				if inStore && len(n.Names) == 1 && n.Names[0].Name == "Seed" {
					return false // the declaration itself
				}
			case *ast.SelectorExpr:
				if x, ok := n.X.(*ast.Ident); ok && names[x.Name] && n.Sel.Name == "Seed" {
					t.Errorf("%s: store.Seed outside a _test.go file", fset.Position(n.Pos()))
				}
			case *ast.Ident:
				if inStore && n.Name == "Seed" {
					t.Errorf("%s: Seed used outside a _test.go file", fset.Position(n.Pos()))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Fatalf("checked only %d files from %s: the walk is not covering the module", checked, root)
	}
}

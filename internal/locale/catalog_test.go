package locale

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestCatalogRejectsInvalidCopy(t *testing.T) {
	for _, data := range []string{`{}`, `{"bad":{"other":"متن"}}`, `{"auth.login.title":{"other":" "}}`, `{`} {
		if _, err := newCatalog([]byte(data)); err == nil {
			t.Errorf("accepted invalid catalog %s", data)
		}
	}
}

// This source gate catches a new UI key or shared error before it can reach a visitor.
func TestCatalogCoversReferencedCopy(t *testing.T) {
	catalog, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	keyReference := regexp.MustCompile(`locale\.T\([^,]+, "([^"]+)"\)`)
	err = filepath.WalkDir("..", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasSuffix(path, "_templ.go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasSuffix(path, ".templ") || strings.HasSuffix(path, ".go") {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, match := range keyReference.FindAllSubmatch(data, -1) {
				if _, ok := catalog.texts[string(match[1])]; !ok {
					t.Errorf("%s references missing key %q", path, match[1])
				}
			}
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_templ.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 4 {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if name != "RenderError" {
				return true
			}
			literal, ok := call.Args[3].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Errorf("%s calls RenderError with copy outside the catalog", path)
				return true
			}
			message, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Errorf("%s has invalid error copy: %v", path, err)
			} else if _, ok := catalog.texts[message]; !ok {
				t.Errorf("%s has untranslated shared error %q", path, message)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

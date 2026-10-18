package lsp

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestGoObjectDetailFiltersAndFormatsImportedTypes(t *testing.T) {
	source := `package api
type Public struct {
	Name string ` + "`json:\"name\"`" + `
	age int
}
type Contract interface {
	Read(value string) error
	privateRead()
}
type Alias = Public
const Limit = 10
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "api.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	configuration := types.Config{Importer: importer.Default()}
	pkg, err := configuration.Check("example/api", fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	qualifier := func(other *types.Package) string {
		if other == pkg {
			return ""
		}
		return other.Name()
	}
	want := map[string][]string{
		"Public":   {"type Public struct {", "Name string `json:\"name\"`", "contains filtered or unexported fields"},
		"Contract": {"type Contract interface {", "Read(value string) error", "contains filtered or unexported methods"},
		"Alias":    {"type Alias = Public"},
		"Limit":    {"const Limit untyped int = 10"},
	}
	for name, fragments := range want {
		detail := goObjectDetail(pkg.Scope().Lookup(name), qualifier)
		for _, fragment := range fragments {
			if !strings.Contains(detail, fragment) {
				t.Errorf("%s detail missing %q: %s", name, fragment, detail)
			}
		}
		if strings.Contains(detail, "age int") || strings.Contains(detail, "privateRead()") {
			t.Errorf("%s detail exposed private member: %s", name, detail)
		}
	}
}

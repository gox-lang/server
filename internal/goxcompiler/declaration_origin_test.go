package gox

import (
	"go/ast"
	"go/token"
	"strings"
	"testing"
)

func TestParseModulePropagatesDeclarationOrigins(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

const salutation = "Hello"

type Profile struct {
	Name string
}

var profile = Profile{Name: "George"}
var count int = 1

func GET(ctx *gox.Context) gox.Page {
	_ = salutation
	_ = profile
	_ = count
	return <main>George</main>
}
`
	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	assertAnchoredName := func(generated string, sourceOffset int) {
		t.Helper()
		found := false
		ast.Inspect(mod.File, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok || ident.Name != generated {
				return true
			}
			origin, ok := mod.Origins.Get(ident)
			if ok && !origin.Exact() && origin.Source.URI == "pages/index.gox" && origin.Source.Range.Start == sourceOffset {
				found = true
			}
			return true
		})
		if !found {
			t.Fatalf("anchored declaration name %s at %d was not found", generated, sourceOffset)
		}
	}

	assertAnchoredName(GoxConstName(mod.Name, "salutation"), strings.Index(src, "salutation"))
	assertAnchoredName(GoxTypeName(mod.Name, "Profile"), strings.Index(src, "Profile"))

	fieldOffsets := map[string]int{
		"profile": strings.Index(src, "profile"),
		"count":   strings.Index(src, "count"),
	}
	foundFields := map[string]bool{}
	for _, decl := range mod.File.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != GoxModuleName(mod.Name) {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok || structType.Fields == nil {
				t.Fatal("generated module declaration is not a struct")
			}
			for _, field := range structType.Fields.List {
				if len(field.Names) != 1 {
					continue
				}
				name := field.Names[0]
				offset, wanted := fieldOffsets[name.Name]
				if !wanted {
					continue
				}
				nameOrigin, ok := mod.Origins.Get(name)
				if !ok || !nameOrigin.Exact() || nameOrigin.Source.Range.Start != offset {
					t.Fatalf("module field %s name origin = %#v, %v", name.Name, nameOrigin, ok)
				}
				fieldOrigin, ok := mod.Origins.Get(field)
				if !ok || fieldOrigin.Exact() || fieldOrigin.Source.Range.Start != offset {
					t.Fatalf("module field %s origin = %#v, %v", name.Name, fieldOrigin, ok)
				}
				foundFields[name.Name] = true
			}
		}
	}
	if !foundFields["profile"] || !foundFields["count"] {
		t.Fatalf("generated module fields = %#v", foundFields)
	}

	foundConstructorKeys := map[string]bool{}
	var staticDecl ast.Decl
	ast.Inspect(mod.File, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.KeyValueExpr:
			key, ok := value.Key.(*ast.Ident)
			if !ok {
				return true
			}
			offset, wanted := fieldOffsets[key.Name]
			if !wanted {
				return true
			}
			origin, ok := mod.Origins.Get(key)
			if ok && !origin.Exact() && origin.Source.Range.Start == offset {
				foundConstructorKeys[key.Name] = true
			}
		case *ast.GenDecl:
			if value.Tok != token.VAR || len(value.Specs) != 1 {
				return true
			}
			spec, ok := value.Specs[0].(*ast.ValueSpec)
			if ok && len(spec.Names) == 1 && strings.HasPrefix(spec.Names[0].Name, "goxStatic") {
				staticDecl = value
			}
		}
		return true
	})
	if !foundConstructorKeys["profile"] || !foundConstructorKeys["count"] {
		t.Fatalf("constructor keys = %#v", foundConstructorKeys)
	}
	staticOrigin, ok := mod.Origins.Get(staticDecl)
	templateOffset := strings.Index(src, "<main>")
	if staticDecl == nil || !ok || staticOrigin.Exact() || staticOrigin.Source.Range.Start != templateOffset {
		t.Fatalf("template static origin = %#v, %v", staticOrigin, ok)
	}
}

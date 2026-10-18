package gox

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"
)

func TestSemanticModelRetainsCallResultAndMethodSelectionTypes(t *testing.T) {
	source := `module page
import "strings"
func Build() {
	value := strings.NewReplacer("a", "b").Replace("abc")
	_ = value
}
`
	module, err := ParseModule("page.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if module.Semantic == nil || module.Semantic.Info == nil {
		t.Fatal("semantic model was not retained")
	}

	var constructor *ast.CallExpr
	var replace *ast.SelectorExpr
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			if selector, ok := value.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "NewReplacer" {
				constructor = value
			}
		case *ast.SelectorExpr:
			if value.Sel.Name == "Replace" {
				replace = value
			}
		}
		return true
	})
	if constructor == nil || replace == nil {
		t.Fatalf("expected constructor call and Replace selector in AST")
	}
	if got := types.TypeString(module.Semantic.Info.TypeOf(constructor), nil); got != "*strings.Replacer" {
		t.Fatalf("NewReplacer call type = %q, want *strings.Replacer", got)
	}
	selection := module.Semantic.Info.Selections[replace]
	if selection == nil {
		t.Fatal("Replace selector has no go/types selection")
	}
	if got := types.TypeString(selection.Obj().Type(), nil); !strings.Contains(got, "func(s string) string") {
		t.Fatalf("Replace method type = %q", got)
	}
}

func TestSemanticModelComposesDeepMethodChains(t *testing.T) {
	source := `module page
import "reflect"
func Build() {
	value := 1
	ok := reflect.ValueOf(&value).Elem().CanSet()
	_ = ok
}
`
	module, err := ParseModule("page.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if module.Semantic == nil || module.Semantic.Info == nil {
		t.Fatal("semantic model was not retained")
	}

	var elemCall *ast.CallExpr
	var canSetCall *ast.CallExpr
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "Elem":
			elemCall = call
		case "CanSet":
			canSetCall = call
		}
		return true
	})
	if elemCall == nil || canSetCall == nil {
		t.Fatal("expected Elem and CanSet calls in AST")
	}
	if got := types.TypeString(module.Semantic.Info.TypeOf(elemCall), nil); got != "reflect.Value" {
		t.Fatalf("Elem call type = %q, want reflect.Value", got)
	}
	if got := types.TypeString(module.Semantic.Info.TypeOf(canSetCall), nil); got != "bool" {
		t.Fatalf("CanSet call type = %q, want bool", got)
	}
}

func TestSemanticModelRetainsGoTypeErrors(t *testing.T) {
	source := `module page
import "strings"
func Build() {
	_ = strings.NewReplacer("a", "b").Replace(123)
}
`
	module, err := ParseModule("page.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if module.Semantic == nil {
		t.Fatal("semantic model was not retained")
	}
	found := false
	for _, typeErr := range module.Semantic.Errors {
		if strings.Contains(typeErr.Error(), "cannot use 123") && strings.Contains(typeErr.Error(), "as string") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing retained Go type error for Replace(123): %v", module.Semantic.Errors)
	}
}

func TestSemanticModelOwnsIndependentSourceLevelAST(t *testing.T) {
	source := `module page
func Helper(value string) string {
	return value
}
`
	module, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if module.Semantic == nil || module.Semantic.File == nil || module.Semantic.FileSet == nil || module.Semantic.Origins == nil || module.Semantic.SourceMap == nil {
		t.Fatal("semantic model does not own a complete source-level representation")
	}
	if module.Semantic.File == module.File {
		t.Fatal("semantic AST must be independent from runtime AST")
	}
	if module.Semantic.Origins == module.Origins {
		t.Fatal("semantic origins must be independent from runtime origins")
	}

	var runtimeHelper, semanticHelper *ast.FuncDecl
	ast.Inspect(module.File, func(node ast.Node) bool {
		fn, ok := node.(*ast.FuncDecl)
		if ok && fn.Name != nil && fn.Name.Name == "Helper" {
			runtimeHelper = fn
			return false
		}
		return true
	})
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		fn, ok := node.(*ast.FuncDecl)
		if ok && fn.Name != nil && fn.Name.Name == "Helper" {
			semanticHelper = fn
			return false
		}
		return true
	})
	if runtimeHelper == nil || semanticHelper == nil {
		t.Fatalf("missing Helper declaration: runtime=%v semantic=%v", runtimeHelper != nil, semanticHelper != nil)
	}
	if runtimeHelper.Recv == nil {
		t.Fatal("runtime Helper should have been transformed into a module method")
	}
	if semanticHelper.Recv != nil {
		t.Fatal("semantic Helper must remain the source-level Go function")
	}
}

func TestSemanticModelIncludesTemplateExpressionsWithExactOrigins(t *testing.T) {
	source := `module page
import (
	"reflect"
	"strings"
)

func Build() interface{} {
	text := "hello"
	view := <span>{{strings.NewReplacer("a", "b").Replace(text)}}</span>
	_ = view
	return <main>{{reflect.ValueOf(&text).Elem().CanSet()}}</main>
}
`
	module, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if module.Semantic == nil || module.Semantic.Info == nil {
		t.Fatal("semantic model was not retained")
	}

	for _, name := range []string{"Replace", "CanSet"} {
		offset := strings.LastIndex(source, name) + len(name)/2
		found, ok := module.SemanticAtOffset(offset)
		if !ok || found.Ident == nil || found.Ident.Name != name {
			t.Fatalf("expected %s semantic identifier inside template, got %#v", name, found)
		}
		if found.Object == nil {
			t.Fatalf("template %s identifier has no go/types object", name)
		}
	}

	calls := map[string]*ast.CallExpr{}
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel != nil && (selector.Sel.Name == "Replace" || selector.Sel.Name == "CanSet") {
			calls[selector.Sel.Name] = call
		}
		return true
	})
	if calls["Replace"] == nil || calls["CanSet"] == nil {
		t.Fatalf("semantic template calls missing: Replace=%v CanSet=%v", calls["Replace"] != nil, calls["CanSet"] != nil)
	}
	if got := types.TypeString(module.Semantic.Info.TypeOf(calls["Replace"]), nil); got != "string" {
		t.Fatalf("value-template Replace call type = %q, want string", got)
	}
	if got := types.TypeString(module.Semantic.Info.TypeOf(calls["CanSet"]), nil); got != "bool" {
		t.Fatalf("return-template CanSet call type = %q, want bool", got)
	}

	for name, want := range map[string]string{
		"Replace": `strings.NewReplacer("a", "b").Replace(text)`,
		"CanSet":  `reflect.ValueOf(&text).Elem().CanSet()`,
	} {
		origin, ok := module.Semantic.Origins.Get(calls[name])
		if !ok || !origin.Exact() {
			t.Fatalf("template %s call origin = %#v, want exact", name, origin)
		}
		if got := source[origin.Source.Range.Start:origin.Source.Range.End]; got != want {
			t.Fatalf("template %s exact source = %q, want %q", name, got, want)
		}
	}
}

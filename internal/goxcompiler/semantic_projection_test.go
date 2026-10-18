package gox

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestSemanticProjectionTypeChecksTemplateExpressionsWithExactOrigins(t *testing.T) {
	source := `module page

import (
    "reflect"
    "strings"
)

func Build() interface{} {
    text := " hello "
    view := <span title={{ strings.TrimSpace(text) }}>{{strings.NewReplacer("a", "b").Replace(text)}}</span>
    _ = view
    return <main data={{ &text }}>{{reflect.ValueOf(&text).Elem().CanSet()}}</main>
}
`
	projection, err := BuildSemanticProjection("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if projection == nil || projection.File == nil || projection.FileSet == nil || projection.SourceMap == nil || projection.Origins == nil {
		t.Fatal("semantic projection is incomplete")
	}
	if strings.Contains(projection.Source, "<span") || strings.Contains(projection.Source, "<main") || strings.Contains(projection.Source, "{{") {
		t.Fatalf("semantic projection still contains template syntax:\n%s", projection.Source)
	}
	if strings.Contains(projection.Source, "goxWriter") {
		t.Fatalf("semantic projection leaked runtime template machinery:\n%s", projection.Source)
	}

	semantic := buildSemanticModel(projection.File, projection.FileSet)
	if semantic == nil || semantic.Info == nil {
		t.Fatal("semantic projection did not produce go/types information")
	}
	if len(semantic.Errors) != 0 {
		t.Fatalf("semantic projection produced unexpected Go errors: %v\nprojection:\n%s", semantic.Errors, projection.Source)
	}

	var replaceCall, canSetCall, elemCall *ast.CallExpr
	var addressOfText []*ast.UnaryExpr
	ast.Inspect(projection.File, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.CallExpr:
			selector, ok := current.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "Replace":
				replaceCall = current
			case "Elem":
				elemCall = current
			case "CanSet":
				canSetCall = current
			}
		case *ast.UnaryExpr:
			if current.Op == token.AND {
				if ident, ok := current.X.(*ast.Ident); ok && ident.Name == "text" {
					addressOfText = append(addressOfText, current)
				}
			}
		}
		return true
	})
	if replaceCall == nil || elemCall == nil || canSetCall == nil {
		t.Fatalf("missing deep-chain calls: Replace=%v Elem=%v CanSet=%v", replaceCall != nil, elemCall != nil, canSetCall != nil)
	}
	if len(addressOfText) != 2 {
		t.Fatalf("address-of text expressions = %d, want 2", len(addressOfText))
	}

	if got := types.TypeString(semantic.Info.TypeOf(replaceCall), nil); got != "string" {
		t.Fatalf("Replace call type = %q, want string", got)
	}
	if got := types.TypeString(semantic.Info.TypeOf(elemCall), nil); got != "reflect.Value" {
		t.Fatalf("Elem call type = %q, want reflect.Value", got)
	}
	if got := types.TypeString(semantic.Info.TypeOf(canSetCall), nil); got != "bool" {
		t.Fatalf("CanSet call type = %q, want bool", got)
	}
	for _, unary := range addressOfText {
		if got := types.TypeString(semantic.Info.TypeOf(unary), nil); got != "*string" {
			t.Fatalf("&text type = %q, want *string", got)
		}
		assertProjectionNodeExactSource(t, source, projection, unary, "&text")
	}
	assertProjectionNodeExactSource(t, source, projection, replaceCall, `strings.NewReplacer("a", "b").Replace(text)`)
	assertProjectionNodeExactSource(t, source, projection, canSetCall, `reflect.ValueOf(&text).Elem().CanSet()`)
}

func TestSemanticProjectionPreservesComponentAttributeGoExpression(t *testing.T) {
	source := `module page

func Build() {
    text := "hello"
    _ = <&Menu text={{ &text }} />
}
`
	projection, err := BuildSemanticProjection("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	semantic := buildSemanticModel(projection.File, projection.FileSet)
	if semantic == nil || semantic.Info == nil {
		t.Fatal("semantic projection did not produce go/types information")
	}

	var address *ast.UnaryExpr
	ast.Inspect(projection.File, func(node ast.Node) bool {
		unary, ok := node.(*ast.UnaryExpr)
		if ok && unary.Op == token.AND {
			if ident, ok := unary.X.(*ast.Ident); ok && ident.Name == "text" {
				address = unary
			}
		}
		return true
	})
	if address == nil {
		t.Fatal("component attribute &text is missing from semantic projection")
	}
	if got := types.TypeString(semantic.Info.TypeOf(address), nil); got != "*string" {
		t.Fatalf("component attribute &text type = %q, want *string", got)
	}
	assertProjectionNodeExactSource(t, source, projection, address, "&text")
}

func TestSemanticProjectionRetainsGoTypeErrorsInsideTemplateExpressions(t *testing.T) {
	source := `module page

import "strings"

func Build() interface{} {
    return <main>{{strings.NewReplacer("a", "b").Replace(123)}}</main>
}
`
	projection, err := BuildSemanticProjection("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	semantic := buildSemanticModel(projection.File, projection.FileSet)
	if semantic == nil {
		t.Fatal("semantic projection did not produce go/types result")
	}
	found := false
	for _, typeErr := range semantic.Errors {
		if strings.Contains(typeErr.Error(), "cannot use 123") && strings.Contains(typeErr.Error(), "as string") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing Go type error from template expression: %v", semantic.Errors)
	}
}

func TestSemanticProjectionHandlesTemplateFragmentsAndCallArguments(t *testing.T) {
	source := `module page

func consume(value interface{}) {}

func Build() {
    text := "hello"
    consume(<>
        <span>{{text}}</span>
    </>)
}
`
	projection, err := BuildSemanticProjection("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	semantic := buildSemanticModel(projection.File, projection.FileSet)
	if semantic == nil || semantic.Info == nil {
		t.Fatal("semantic projection did not produce go/types information")
	}
	if len(semantic.Errors) != 0 {
		t.Fatalf("fragment projection produced unexpected Go errors: %v\nprojection:\n%s", semantic.Errors, projection.Source)
	}

	var templateText *ast.Ident
	ast.Inspect(projection.File, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name != "text" {
			return true
		}
		origin, ok := projection.Origins.Get(ident)
		if ok && origin.Exact() && origin.Source.URI == "pages/index.gox" && origin.Source.Range.Start > strings.Index(source, "consume(") {
			templateText = ident
		}
		return true
	})
	if templateText == nil {
		t.Fatal("template fragment text identifier is missing exact source provenance")
	}
	if got := types.TypeString(semantic.Info.TypeOf(templateText), nil); got != "string" {
		t.Fatalf("fragment text type = %q, want string", got)
	}
}

func assertProjectionNodeExactSource(t *testing.T, source string, projection *SemanticProjection, node ast.Node, want string) {
	t.Helper()
	origin, ok := projection.Origins.Get(node)
	if !ok {
		t.Fatalf("%T has no semantic projection origin", node)
	}
	if !origin.Exact() {
		t.Fatalf("%T origin is not exact: %#v", node, origin)
	}
	if origin.Source.URI != "pages/index.gox" {
		t.Fatalf("%T origin URI = %q, want pages/index.gox", node, origin.Source.URI)
	}
	rng := origin.Source.Range
	if !rng.Valid() || rng.End > len(source) {
		t.Fatalf("%T source range %#v is invalid for source length %d", node, rng, len(source))
	}
	if got := source[rng.Start:rng.End]; got != want {
		t.Fatalf("%T exact source = %q, want %q", node, got, want)
	}
}

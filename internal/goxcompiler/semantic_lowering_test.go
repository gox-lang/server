package gox

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"
)

func TestSemanticProjectionLowersPrimaryGoxMarkerContracts(t *testing.T) {
	source := `module page

import (
    "reflect"
    "strings"
)

type Page interface{}
type Action interface{}
type Component interface{}
type WebSocket interface{}
type Channel interface{}

func RenderPage(text string) Page {
    if text != "" {
        return strings.NewReplacer("a", "b").Replace(text)
    }
}

func Save(text string) Action {
    if text != "" {
        return reflect.ValueOf(&text).Elem().CanSet()
    }
    return
}

func Card(text string) Component {
    return strings.TrimSpace(text)
}

func Socket() WebSocket {}
func Room() Channel {}
`
	projection, err := BuildSemanticProjection("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}

	modes := semanticProjectionReturnModes(projection)
	for name, want := range map[string]SemanticGoxReturnMode{
		"Page":      SemanticGoxReturnFlexibleOptional,
		"Action":    SemanticGoxReturnFlexibleOptional,
		"Component": SemanticGoxReturnFlexibleRequired,
		"WebSocket": SemanticGoxReturnVoid,
		"Channel":   SemanticGoxReturnVoid,
	} {
		if got := modes[name]; got != want {
			t.Fatalf("%s semantic return mode = %q, want %q", name, got, want)
		}
	}

	semantic := buildSemanticModel(projection.File, projection.FileSet)
	if semantic == nil || semantic.Info == nil {
		t.Fatal("semantic projection did not produce go/types information")
	}
	if len(semantic.Errors) != 0 {
		t.Fatalf("GOX marker lowering produced unexpected Go errors: %v", semantic.Errors)
	}

	calls := semanticCallTypesBySelector(projection, semantic)
	if got := calls["Replace"]; got != "string" {
		t.Fatalf("Page return Replace call type = %q, want string", got)
	}
	if got := calls["CanSet"]; got != "bool" {
		t.Fatalf("Action return CanSet call type = %q, want bool", got)
	}
	if got := calls["TrimSpace"]; got != "string" {
		t.Fatalf("Component return TrimSpace call type = %q, want string", got)
	}
}

func TestSemanticProjectionComponentRetainsRequiredReturnContract(t *testing.T) {
	source := `module page

type Component interface{}

func Card(ok bool) Component {
    if ok {
        return "ready"
    }
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
	if !semanticErrorsContain(semantic.Errors, "missing return") {
		t.Fatalf("Component without a terminating return must retain missing-return semantics: %v", semantic.Errors)
	}
}

func TestSemanticProjectionComponentNamedResultKeepsBareReturn(t *testing.T) {
	source := `module page

type Component interface{}

func Card() (result Component) {
    return
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
	if len(semantic.Errors) != 0 {
		t.Fatalf("named Component result should preserve Go bare-return behavior: %v", semantic.Errors)
	}
}

func TestSemanticProjectionPageAndActionAllowEmptyBodies(t *testing.T) {
	source := `module page

type Page interface{}
type Action interface{}

func Index() Page {}
func Login() Action {}
`
	projection, err := BuildSemanticProjection("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	semantic := buildSemanticModel(projection.File, projection.FileSet)
	if semantic == nil {
		t.Fatal("semantic projection did not produce go/types result")
	}
	if semanticErrorsContain(semantic.Errors, "missing return") {
		t.Fatalf("Page/Action empty bodies must not produce missing-return errors: %v", semantic.Errors)
	}
	if len(semantic.Errors) != 0 {
		t.Fatalf("Page/Action empty bodies produced unexpected errors: %v", semantic.Errors)
	}
}

func TestSemanticProjectionWebSocketAndChannelRejectReturnValues(t *testing.T) {
	source := `module websocket

type WebSocket interface{}
type Channel interface{}

func Socket() WebSocket { return true }
func Room() Channel { return "value" }
`
	projection, err := BuildSemanticProjection("websocket/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	semantic := buildSemanticModel(projection.File, projection.FileSet)
	if semantic == nil {
		t.Fatal("semantic projection did not produce go/types result")
	}
	if !semanticErrorsContain(semantic.Errors, "too many return values") {
		t.Fatalf("WebSocket/Channel return values must remain invalid Go returns: %v", semantic.Errors)
	}
}

func TestSemanticProjectionLowersEventReplyFunctionLiteral(t *testing.T) {
	source := `module websocket

type EventReply interface{}

func Build() {
    callback := func(text string) EventReply {
        return len(text) > 0
    }
    _ = callback
}
`
	projection, err := BuildSemanticProjection("websocket/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	semantic := buildSemanticModel(projection.File, projection.FileSet)
	if semantic == nil {
		t.Fatal("semantic projection did not produce go/types result")
	}
	if len(semantic.Errors) != 0 {
		t.Fatalf("EventReply callback should accept an arbitrary single result: %v", semantic.Errors)
	}
	if got := semanticProjectionReturnModes(projection)["EventReply"]; got != SemanticGoxReturnFlexibleRequired {
		t.Fatalf("EventReply semantic return mode = %q, want %q", got, SemanticGoxReturnFlexibleRequired)
	}
}

func TestSemanticProjectionRecognizesQualifiedGoxComponent(t *testing.T) {
	source := `module page

import gox "github.com/gox-lang/gox"

func Card() gox.Component {
    return <span>Hello</span>
}
`
	projection, err := BuildSemanticProjection("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if got := semanticProjectionReturnModes(projection)["Component"]; got != SemanticGoxReturnFlexibleRequired {
		t.Fatalf("gox.Component semantic return mode = %q, want %q", got, SemanticGoxReturnFlexibleRequired)
	}

	var markerType ast.Expr
	for _, declaration := range projection.File.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || value.Type == nil {
				continue
			}
			selector, ok := value.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Component" {
				continue
			}
			markerType = selector
		}
	}
	if markerType == nil {
		t.Fatal("lowering did not retain an exact semantic use of gox.Component")
	}
	origin, ok := projection.Origins.Get(markerType)
	if !ok || !origin.Exact() {
		t.Fatalf("retained gox.Component marker origin = %#v, want exact source", origin)
	}
	if got := source[origin.Source.Range.Start:origin.Source.Range.End]; got != "gox.Component" {
		t.Fatalf("retained marker source = %q, want gox.Component", got)
	}
}

func semanticProjectionReturnModes(projection *SemanticProjection) map[string]SemanticGoxReturnMode {
	modes := make(map[string]SemanticGoxReturnMode)
	if projection == nil {
		return modes
	}
	for _, lowered := range projection.GoxReturns {
		modes[lowered.ReturnName] = lowered.Mode
	}
	return modes
}

func semanticCallTypesBySelector(projection *SemanticProjection, semantic *SemanticModel) map[string]string {
	result := make(map[string]string)
	if projection == nil || projection.File == nil || semantic == nil || semantic.Info == nil {
		return result
	}
	ast.Inspect(projection.File, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil {
			return true
		}
		if typ := semantic.Info.TypeOf(call); typ != nil {
			result[selector.Sel.Name] = types.TypeString(typ, nil)
		}
		return true
	})
	return result
}

func semanticErrorsContain(errors []error, fragment string) bool {
	for _, err := range errors {
		if err != nil && strings.Contains(err.Error(), fragment) {
			return true
		}
	}
	return false
}

func TestSemanticProjectionComponentTemplateUsesSameGoSemanticsInsideInterpolation(t *testing.T) {
	source := `module page

import "strings"

type Component interface{}

func Card(text string) Component {
    return <span>{{strings.NewReplacer("a", "b").Replace(text)}}</span>
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
		t.Fatalf("Component template projection produced unexpected Go errors: %v", semantic.Errors)
	}
	calls := semanticCallTypesBySelector(projection, semantic)
	if got := calls["Replace"]; got != "string" {
		t.Fatalf("Component template Replace call type = %q, want string", got)
	}
}

func TestSemanticProjectionDoesNotLowerOrdinaryGoReturns(t *testing.T) {
	source := `module page

func Build() string {
    return 123
}
`
	projection, err := BuildSemanticProjection("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.GoxReturns) != 0 {
		t.Fatalf("ordinary Go function was classified as GOX semantic return: %#v", projection.GoxReturns)
	}
	semantic := buildSemanticModel(projection.File, projection.FileSet)
	if semantic == nil {
		t.Fatal("semantic projection did not produce go/types result")
	}
	if !semanticErrorsContain(semantic.Errors, "cannot use 123") || !semanticErrorsContain(semantic.Errors, "as string") {
		t.Fatalf("ordinary Go return mismatch must remain a Go error: %v", semantic.Errors)
	}
}

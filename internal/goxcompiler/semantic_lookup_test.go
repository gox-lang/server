package gox

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"
)

func TestSemanticAtOffsetFindsTypedCallReceiverInChain(t *testing.T) {
	source := `module page
import "strings"

func semanticTest() string {
    return strings.NewReplacer("a", "b").Replace("abc")
}
`
	mod := parseSemanticLookupModule(t, source)
	offset := strings.Index(source, `NewReplacer`) + len(`NewReplacer`)
	found, ok := mod.SemanticAtOffset(offset)
	if !ok {
		t.Fatal("expected semantic node at NewReplacer")
	}
	if found.Ident == nil || found.Ident.Name != "NewReplacer" {
		t.Fatalf("expected NewReplacer identifier, got %T", found.Node)
	}
	if found.Object == nil {
		t.Fatal("expected go/types object for NewReplacer")
	}
}

func TestSemanticAtOffsetFindsMethodSelectionInCallChain(t *testing.T) {
	source := `module page
import "strings"

func semanticTest() string {
    return strings.NewReplacer("a", "b").Replace("abc")
}
`
	mod := parseSemanticLookupModule(t, source)
	offset := strings.Index(source, `Replace("abc")`) + 2
	found, ok := mod.SemanticAtOffset(offset)
	if !ok {
		t.Fatal("expected semantic node at Replace")
	}
	if found.Ident == nil || found.Ident.Name != "Replace" {
		t.Fatalf("expected Replace identifier, got %T", found.Node)
	}
	obj, ok := found.Object.(*types.Func)
	if !ok || obj.Name() != "Replace" {
		t.Fatalf("expected Replace method object, got %T %v", found.Object, found.Object)
	}

	// The enclosing SelectorExpr must independently resolve to the method
	// selection. Locate it through the exact source origin rather than by
	// reconstructing qualifier/name strings.
	var selection *types.Selection
	ast.Inspect(mod.Semantic.File, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if ok && sel.Sel == found.Ident {
			selection = mod.Semantic.Info.Selections[sel]
			return false
		}
		return true
	})
	if selection == nil || selection.Obj().Name() != "Replace" {
		t.Fatalf("expected semantic Replace selection, got %v", selection)
	}
}

func TestSemanticAtOffsetComposesDeepChainWithoutQualifierStrings(t *testing.T) {
	source := `module page
import "reflect"

func semanticTest() bool {
    value := "x"
    return reflect.ValueOf(&value).Elem().CanSet()
}
`
	mod := parseSemanticLookupModule(t, source)
	offset := strings.Index(source, `CanSet`) + 3
	found, ok := mod.SemanticAtOffset(offset)
	if !ok || found.Ident == nil || found.Ident.Name != "CanSet" {
		t.Fatalf("expected CanSet semantic identifier, got %#v", found)
	}
	if found.Object == nil {
		t.Fatal("expected typed CanSet object")
	}
}

func parseSemanticLookupModule(t *testing.T, source string) *Module {
	t.Helper()
	mod, err := ParseModule("/pages/index.gox", source)
	if err != nil {
		t.Fatalf("ParseModule: %v", err)
	}
	if mod.Semantic == nil || mod.Semantic.Info == nil {
		t.Fatal("expected semantic model")
	}
	return mod
}

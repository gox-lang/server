package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticCompletionResolvesCallExpressionReceiver(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() {
    _ = strings.NewReplacer("a", "b").
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	server := NewServer()
	items := completionItemsAtEnd(t, server, document, `strings.NewReplacer("a", "b").`)
	assertHasCompletion(t, items, "Replace")
}

func TestSemanticHoverResolvesMethodOnCallExpression(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() {
    _ = strings.NewReplacer("a", "b").Replace("a")
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	server := NewServer()
	hover := hoverAtOffset(t, server, document, strings.LastIndex(source, "Replace")+1)
	if hover == nil || !strings.Contains(hover.Contents.Value, "Replace(s string) string") {
		t.Fatalf("Replace hover = %#v", hover)
	}
}

func TestSemanticSignatureHelpResolvesMethodOnCallExpression(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() {
    _ = strings.NewReplacer("a", "b").Replace("a")
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	server := NewServer()
	open := strings.LastIndex(source, "Replace(") + len("Replace(")
	help := signatureAtOffset(t, server, document, open)
	if help == nil || len(help.Signatures) != 1 || !strings.Contains(help.Signatures[0].Label, "Replace(s string) string") {
		t.Fatalf("Replace signature help = %#v", help)
	}
}

func TestSemanticFeaturesComposeDeepCallChain(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	source := `module page
import "reflect"
func Build() {
    value := "x"
    _ = reflect.ValueOf(&value).Elem().
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	server := NewServer()
	items := completionItemsAtEnd(t, server, document, `reflect.ValueOf(&value).Elem().`)
	assertHasCompletion(t, items, "CanSet")
	assertHasCompletion(t, items, "Field")
}

func assertHasCompletion(t *testing.T, items []CompletionItem, label string) {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return
		}
	}
	t.Fatalf("completion %q missing from %#v", label, items)
}

func TestLexicalCompletionKeepsFunctionScopeInsideCallArguments(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() {
    text := "hello"
    _ = strings.NewReplacer("a", "b").Replace(te)
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	items := completionItemsAtEnd(t, NewServer(), document, "te")
	assertHasCompletion(t, items, "text")
}

func TestSemanticDiagnosticsResolveMethodOnCallExpression(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() {
    _ = strings.NewReplacer("a", "b").Replace(123)
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	server := NewServer()
	diagnostics := server.diagnostics(document)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "type-mismatch" && strings.Contains(diagnostic.Message, "as string in argument 1 to Replace") {
			return
		}
	}
	t.Fatalf("missing chained-call type diagnostic in %#v", diagnostics)
}

func TestSemanticDefinitionResolvesMethodOnCallExpression(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() {
    _ = strings.NewReplacer("a", "b").Replace("a")
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	server := NewServer()
	offset := strings.LastIndex(source, "Replace") + 1
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	locations := server.definitionLocations(document, position)
	if len(locations) == 0 || !strings.HasSuffix(locations[0].URI, ".go") {
		t.Fatalf("Replace definition = %#v", locations)
	}
}

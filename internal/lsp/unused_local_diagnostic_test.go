package lsp

import (
	"strings"
	"testing"
)

func TestSemanticDiagnosticsReportUnusedLocalVariables(t *testing.T) {
	source := `module page
func Build(parameter string) {
	used := parameter
	_ = used
	unused := 1
	var another string
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	want := map[string]int{
		"declared and not used: unused":  strings.Index(source, "unused :="),
		"declared and not used: another": strings.Index(source, "another string"),
	}
	assertUnusedLocalDiagnostics(t, document, diagnostics, want)
}

func TestSemanticDiagnosticsAllowUnusedParametersReceiversAndResults(t *testing.T) {
	source := `module page
type User struct{}
func (receiver User) Build(parameter string) (result string) {
	return "George"
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "unused-local" {
			t.Fatalf("diagnostics = %#v", server.diagnostics(document))
		}
	}
}

func TestSemanticDiagnosticsAllowUnusedPageContextAfterCompilerTransforms(t *testing.T) {
	source := `module page
func GET(ctx *gox.Context) gox.Page {
	return <main>George</main>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "unused-local" && diagnostic.Message == "declared and not used: ctx" {
			t.Fatalf("page context was reported as unused: %#v", diagnostic)
		}
	}
}

func TestSemanticDiagnosticsResolveShadowedLocalUses(t *testing.T) {
	source := `module page
func Build() {
	value := 1
	{
		value := 2
		_ = value
	}
	_ = value
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "unused-local" {
			t.Fatalf("diagnostics = %#v", server.diagnostics(document))
		}
	}
}

func TestSemanticDiagnosticsCountTemplateLocalUses(t *testing.T) {
	source := `module page
func Card() gox.Component {
	name := "George"
	return <span>{{ name }}</span>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := assertAndRemoveUndefinedGoxDiagnostics(t, document, server.diagnostics(document), strings.Index(source, "gox.Component"))
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "unused-local" {
			t.Fatalf("diagnostics = %#v", diagnostics)
		}
	}
}

func assertUnusedLocalDiagnostics(t *testing.T, document Document, diagnostics []Diagnostic, want map[string]int) {
	t.Helper()
	if len(diagnostics) != len(want) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		offset, err := document.Offset(diagnostic.Range.Start)
		expected, ok := want[diagnostic.Message]
		if !ok || err != nil || diagnostic.Code != "unused-local" || diagnostic.Source != "gox" || offset != expected {
			t.Errorf("diagnostic = %#v at %d", diagnostic, offset)
		}
		delete(want, diagnostic.Message)
	}
	if len(want) != 0 {
		t.Fatalf("missing diagnostics = %#v", want)
	}
}

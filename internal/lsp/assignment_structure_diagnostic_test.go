package lsp

import (
	"strings"
	"testing"
)

func TestSemanticDiagnosticsValidateShortDeclarationsAndRedeclarations(t *testing.T) {
	source := `module page
func Build() {
	name := "George"
	name := "Milo"
	var age int
	var age int
	name, alias := "Milo", "George"
	_, _, _ = name, age, alias
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "no-new-variables", "no new variables on left side of :=", strings.Index(source, "name := \"Milo\""))
	assertDiagnosticAt(t, document, diagnostics, "redeclared-local", "age redeclared in this block", strings.LastIndex(source, "age int"))
}

func TestSemanticDiagnosticsReportTopLevelRedeclarationsWithoutUndefinedDeclarations(t *testing.T) {
	source := `module page
func Build() {}
func Build() {}
func Build() {}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	first := strings.Index(source, "Build")
	second := strings.Index(source[first+1:], "Build") + first + 1
	third := strings.Index(source[second+1:], "Build") + second + 1
	assertDiagnosticAt(t, document, diagnostics, "redeclared-symbol", "Build redeclared in this block", second)
	assertDiagnosticAt(t, document, diagnostics, "redeclared-symbol", "Build redeclared in this block", third)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "undefined-identifier" && diagnostic.Message == "undefined: Build" {
			t.Fatalf("function declaration reported as undefined: %#v", diagnostic)
		}
	}
}

func TestSemanticDiagnosticsAllowShadowingAndMixedShortDeclarations(t *testing.T) {
	source := `module page
func Build() {
	name := "George"
	name, age := "Milo", 30
	{
		name := "Nested"
		_ = name
	}
	_, _ = name, age
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	if diagnostics := server.diagnostics(document); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestSemanticDiagnosticsValidateAssignmentArity(t *testing.T) {
	source := `module page
func Lookup() (string, bool) { return "George", true }
func Name() string { return "George" }
func Build() {
	name := Name()
	var first, second = Name()
	name, ok := Name()
	name = Lookup()
	name, ok = Lookup()
	name, ok, extra := Lookup()
	_, _, _ = name, ok, extra
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "assignment-mismatch", "assignment mismatch: 2 variables but Name returns 1 value", strings.Index(source, "first, second"))
	assertDiagnosticAt(t, document, diagnostics, "assignment-mismatch", "assignment mismatch: 2 variables but Name returns 1 value", strings.Index(source, "name, ok := Name()"))
	assertDiagnosticAt(t, document, diagnostics, "assignment-mismatch", "assignment mismatch: 1 variable but Lookup returns 2 values", strings.Index(source, "name = Lookup()"))
	assertDiagnosticAt(t, document, diagnostics, "assignment-mismatch", "assignment mismatch: 3 variables but Lookup returns 2 values", strings.Index(source, "name, ok, extra := Lookup()"))
}

func assertDiagnosticAt(t *testing.T, document Document, diagnostics []Diagnostic, code, message string, offset int) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		actual, err := document.Offset(diagnostic.Range.Start)
		if err == nil && diagnostic.Code == code && diagnostic.Message == message && actual == offset {
			return
		}
	}
	t.Fatalf("missing %s at %d in %#v", code, offset, diagnostics)
}

func TestSemanticDiagnosticsAllowShortDeclarationsInStatementInitializers(t *testing.T) {
	source := `module page
func Build() {
	if err := recover(); err != nil {
		_ = err
	}
	for i := 0; i < 1; i++ {
		_ = i
	}
	switch value := 1; value {
	case 1:
		_ = value
	}
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "no-new-variables" {
			t.Fatalf("statement initializer reported as having no new variables: %#v", diagnostic)
		}
	}
}

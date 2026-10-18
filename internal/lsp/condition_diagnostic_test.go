package lsp

import (
	"strings"
	"testing"
)

func TestSemanticDiagnosticsValidateBooleanConditionsAndOperators(t *testing.T) {
	source := `module page
func Build(name string, count int, enabled bool) {
	if name {}
	for count {}
	_ = !count
	_ = enabled && name
	_ = count == "30"
	_ = enabled < false
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "non-boolean-condition", "non-boolean condition in if statement (type string)", indexAfter(source, "name", "if "))
	assertDiagnosticAt(t, document, diagnostics, "non-boolean-condition", "non-boolean condition in for statement (type int)", indexAfter(source, "count", "for "))
	assertDiagnosticAt(t, document, diagnostics, "invalid-boolean-operand", "operator ! requires boolean operand; got int", indexAfter(source, "count", "!"))
	assertDiagnosticAt(t, document, diagnostics, "invalid-boolean-operand", "operator && requires boolean operand; got string", indexAfter(source, "name", "&&"))
	assertDiagnosticAt(t, document, diagnostics, "mismatched-comparison", "invalid operation: mismatched types int and untyped string", strings.Index(source, "count == \"30\""))
	assertDiagnosticAt(t, document, diagnostics, "invalid-comparison", "operator < not defined on bool and untyped bool", strings.Index(source, "enabled < false"))
}

func TestSemanticDiagnosticsAllowValidBooleanExpressions(t *testing.T) {
	source := `module page
func Build(count int, enabled bool) {
	if enabled && count > 0 {}
	for enabled || count == 0 { break }
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		switch diagnostic.Code {
		case "non-boolean-condition", "invalid-boolean-operand", "mismatched-comparison", "invalid-comparison":
			t.Fatalf("valid boolean expression received diagnostic: %#v", diagnostic)
		}
	}
}

func TestSemanticDiagnosticsValidateTemplateIfConditions(t *testing.T) {
	source := `module page
import "github.com/gox-lang/gox"
func Card() gox.Component {
	return <:if condition={{ "George" }}><span>Invalid</span></:if>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "non-boolean-condition", "non-boolean condition in :if directive (type untyped string)", strings.Index(source, "\"George\""))
}

package lsp

import (
	"strings"
	"testing"
)

func TestSemanticDiagnosticsRejectBlankIdentifierAsValueOrType(t *testing.T) {
	source := `module page
func Format(name string, count int) string { return name }
func Build(total int) string {
	_ = Format("George", total)
	value := Format(_, total)
	return _
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	first := strings.Index(source, "Format(_, total)") + len("Format(")
	second := strings.LastIndex(source, "_")
	assertDiagnosticAt(t, document, diagnostics, "invalid-blank-identifier", "cannot use _ as value or type", first)
	assertDiagnosticAt(t, document, diagnostics, "invalid-blank-identifier", "cannot use _ as value or type", second)
}

func TestSemanticDiagnosticsAllowBlankIdentifierDiscards(t *testing.T) {
	source := `module page
func Pair() (string, bool) { return "George", true }
func Build(values []string) {
	_, ok := Pair()
	for _, value := range values {
		_ = value
	}
	_ = ok
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "invalid-blank-identifier" {
			t.Fatalf("blank discard reported as invalid: %#v", diagnostic)
		}
	}
}

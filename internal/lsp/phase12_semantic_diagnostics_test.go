package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase12SemanticErrorsMapTemplateExpressionsToGOXSource(t *testing.T) {
	source := `module page
func Card() {
	view := <span>{{ missingName }}</span>
	_ = view
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	module, ok := server.semanticModule(document)
	if !ok {
		t.Fatal("semantic module unavailable")
	}
	want := strings.Index(source, "missingName")
	found := false
	for _, semanticErr := range semanticSourceErrors(module) {
		if semanticErr.Offset == want && strings.Contains(semanticErr.Message, "undefined: missingName") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("semantic errors = %#v, want exact template error at %d", semanticSourceErrors(module), want)
	}

	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "undefined-identifier", "undefined: missingName", want)
}

func TestPhase12SemanticCallDiagnosticsOwnTemplateArguments(t *testing.T) {
	source := `module page
func Join(left string, right string) string { return left + right }
func Card() {
	view := <span>{{ Join(123, "ok") }}</span>
	_ = view
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "type-mismatch",
		"cannot use 123 (type untyped int) as string in argument 1 to Join", strings.Index(source, "123"))
}

func TestPhase12SemanticDiagnosticsOwnImportedGoxSelectorsAndArguments(t *testing.T) {
	root := t.TempDir()
	usersSource := `module users
type User struct { Name string }
func Lookup(id int) User { return User{} }
`
	pageSource := `module page
import users "./users.gox"
func Build(user users.User) {
	_ = users.Lookup("bad")
	_ = user.Missing
}
`
	if err := os.WriteFile(filepath.Join(root, "users.gox"), []byte(usersSource), 0o600); err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document not indexed")
	}
	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "type-mismatch",
		`cannot use "bad" (type untyped string) as int in argument 1 to users.Lookup`, strings.Index(pageSource, `"bad"`))
	assertDiagnosticAt(t, document, diagnostics, "undefined-selector",
		"undefined selector: user.Missing", strings.Index(pageSource, "Missing"))
}

func TestPhase12SemanticUnusedLocalSeesTemplateUse(t *testing.T) {
	source := `module page
func Card() {
	used := "ok"
	unused := "bad"
	view := <span>{{ used }}</span>
	_ = view
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "unused-local" && diagnostic.Message == "declared and not used: used" {
			t.Fatalf("template use was not visible to semantic diagnostics: %#v", diagnostics)
		}
	}
	assertDiagnosticAt(t, document, diagnostics, "unused-local", "declared and not used: unused", strings.Index(source, "unused :="))
}

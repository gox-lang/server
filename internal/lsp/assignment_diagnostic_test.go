package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticDiagnosticsReportAssignmentTypeMismatches(t *testing.T) {
	source := `module page
type User struct { Name string }
func Build(user User, name string) {
	var age bool = 123
	var title string = "George"
	var enabled bool = name
	_ = age
	_ = enabled
	user.Name = 42
	user.Name = title
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	want := map[string]int{
		"cannot use 123 (type untyped int) as bool in assignment":  strings.Index(source, "123"),
		"cannot use name (type string) as bool in assignment":      strings.Index(source, "name\n"),
		"cannot use 42 (type untyped int) as string in assignment": strings.Index(source, "42"),
	}
	if len(diagnostics) != len(want) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		offset, err := document.Offset(diagnostic.Range.Start)
		if err != nil || diagnostic.Code != "type-mismatch" || diagnostic.Source != "gox" || want[diagnostic.Message] != offset {
			t.Errorf("diagnostic = %#v at %d", diagnostic, offset)
		}
		delete(want, diagnostic.Message)
	}
	if len(want) != 0 {
		t.Fatalf("missing diagnostics = %#v", want)
	}
}

func TestSemanticDiagnosticsAcceptCompatibleUntypedValues(t *testing.T) {
	source := `module page
func Build() {
	var enabled bool = true
	var name string = "George"
	var count int64 = 42
	var ratio float64 = 2.5
	_, _, _, _ = enabled, name, count, ratio
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	if diagnostics := server.diagnostics(document); len(diagnostics) != 0 {
		t.Fatalf("compatible assignment diagnostics = %#v", diagnostics)
	}
}

func TestSemanticDiagnosticsValidateImportedStructLiteralFields(t *testing.T) {
	root := t.TempDir()
	usersSource := `module users
type User struct {
	Name string
	age int
}
`
	pageSource := `module page
import users "./users.gox"
func Build(name string) {
	_ = users.User{
		Name: 123,
		age: 30,
		apodo: true,
	}
	_ = users.User{Name: name}
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
		t.Fatal("page document was not indexed")
	}
	diagnostics := server.diagnostics(document)
	want := map[string]struct {
		code   string
		offset int
	}{
		"cannot use 123 (type untyped int) as string in field Name":                 {"type-mismatch", strings.Index(pageSource, "123")},
		"cannot refer to unexported field age in struct literal of type users.User": {"unexported-field", strings.Index(pageSource, "age:")},
		"unknown field apodo in struct literal of type users.User":                  {"unknown-field", strings.Index(pageSource, "apodo:")},
	}
	assertStructLiteralDiagnostics(t, document, diagnostics, want)
}

func TestSemanticDiagnosticsValidateLocalStructLiteralFields(t *testing.T) {
	source := `module page
type User struct {
	Name string
	age int
}
func Build(name string) {
	_ = User{Name: name, age: 30}
	_ = &User{Name: false, Missing: 1}
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	want := map[string]struct {
		code   string
		offset int
	}{
		"cannot use false (type untyped bool) as string in field Name": {"type-mismatch", strings.Index(source, "false")},
		"unknown field Missing in struct literal of type User":         {"unknown-field", strings.Index(source, "Missing")},
	}
	assertStructLiteralDiagnostics(t, document, diagnostics, want)
}

func assertStructLiteralDiagnostics(t *testing.T, document Document, diagnostics []Diagnostic, want map[string]struct {
	code   string
	offset int
}) {
	t.Helper()
	if len(diagnostics) != len(want) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		expected, exists := want[diagnostic.Message]
		offset, err := document.Offset(diagnostic.Range.Start)
		if !exists || err != nil || diagnostic.Code != expected.code || diagnostic.Source != "gox" || offset != expected.offset {
			t.Errorf("diagnostic = %#v at %d", diagnostic, offset)
		}
		delete(want, diagnostic.Message)
	}
	if len(want) != 0 {
		t.Fatalf("missing diagnostics = %#v", want)
	}
}

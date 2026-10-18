package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticDiagnosticsValidateLocalCallArguments(t *testing.T) {
	source := `module page
func Join(left string, right string) string { return left + right }
func Sum(first int, rest ...int) int { return first }
func Build(name string, values []int) {
	_ = Join("George")
	_ = Join("George", name, "extra")
	_ = Join(123, name)
	_ = Sum()
	_ = Sum(1, 2, 3)
	_ = Sum(1, values...)
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
		"wrong number of arguments in call to Join: have 1, want 2":         {"argument-count", strings.Index(source, `Join("George")`)},
		"wrong number of arguments in call to Join: have 3, want 2":         {"argument-count", strings.Index(source, `Join("George", name, "extra")`)},
		"cannot use 123 (type untyped int) as string in argument 1 to Join": {"type-mismatch", strings.Index(source, "123")},
		"wrong number of arguments in call to Sum: have 0, want at least 1": {"argument-count", strings.Index(source, "Sum()")},
	}
	assertCallDiagnostics(t, document, diagnostics, want)
}

func TestSemanticDiagnosticsValidateMethodsAndImportedGOXCalls(t *testing.T) {
	root := t.TempDir()
	usersSource := `module users
type User struct { Name string }
func Lookup(id int, strict bool) (User, error) { return User{}, nil }
`
	pageSource := `module page
import users "./users.gox"
type Profile struct { Name string }
func (profile *Profile) Rename(name string, trim bool) { profile.Name = name }
func Build(profile *Profile) {
	profile.Rename(123, true)
	profile.Rename("George")
	_, _ = users.Lookup("bad", false)
	_, _ = users.Lookup(10)
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
		"cannot use 123 (type untyped int) as string in argument 1 to profile.Rename": {"type-mismatch", strings.Index(pageSource, "123")},
		"wrong number of arguments in call to profile.Rename: have 1, want 2":         {"argument-count", strings.Index(pageSource, `profile.Rename("George")`)},
		`cannot use "bad" (type untyped string) as int in argument 1 to users.Lookup`: {"type-mismatch", strings.Index(pageSource, `"bad"`)},
		"wrong number of arguments in call to users.Lookup: have 1, want 2":           {"argument-count", strings.Index(pageSource, "users.Lookup(10)")},
	}
	assertCallDiagnostics(t, document, diagnostics, want)
}

func TestSemanticDiagnosticsValidateImportedGoPackageCalls(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import "strings"
import "fmt"
func Build(name string, values []any) {
	_ = strings.ReplaceAll(name, "a")
	_ = strings.Repeat(name, true)
	_ = fmt.Sprintf("%s: %s", name, name)
	_ = fmt.Sprintf("%v", values...)
	_ = fmt.Sprintf()
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
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
		"wrong number of arguments in call to strings.ReplaceAll: have 2, want 3":    {"argument-count", strings.Index(pageSource, "strings.ReplaceAll")},
		"cannot use true (type untyped bool) as int in argument 2 to strings.Repeat": {"type-mismatch", strings.Index(pageSource, "true")},
		"wrong number of arguments in call to fmt.Sprintf: have 0, want at least 1":  {"argument-count", strings.LastIndex(pageSource, "fmt.Sprintf")},
	}
	assertCallDiagnostics(t, document, diagnostics, want)
}

func TestSemanticDiagnosticsRejectNonCallableFieldsAndAllowFunctionFields(t *testing.T) {
	source := `module page
type User struct {
	Name string
	Format func(string) string
}
func Build(user User) {
	_ = user.Name()
	_ = user.Format("George")
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	found := false
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "non-callable" && diagnostic.Message == "invalid operation: cannot call non-function user.Name (type string)" {
			offset, err := document.Offset(diagnostic.Range.Start)
			if err != nil || offset != strings.Index(source, "user.Name") {
				t.Fatalf("non-callable range = %#v", diagnostic.Range)
			}
			found = true
		}
		if diagnostic.Code == "non-callable" && strings.Contains(diagnostic.Message, "user.Format") {
			t.Fatalf("function field reported as non-callable: %#v", diagnostic)
		}
	}
	if !found {
		t.Fatalf("missing non-callable field diagnostic in %#v", diagnostics)
	}
}

func TestSemanticDiagnosticsRejectImportedNonCallableFieldInsideTemplate(t *testing.T) {
	root := t.TempDir()
	usersSource := `module users
type User struct { Name string }
`
	pageSource := `module page
import (
	users "./users.gox"
)
func Card(user users.User) {
	content := <h2>{{ user.Name() }}</h2>
	_ = content
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
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "non-callable" && diagnostic.Message == "invalid operation: cannot call non-function user.Name (type string)" {
			offset, err := document.Offset(diagnostic.Range.Start)
			if err != nil || offset != strings.Index(pageSource, "user.Name") {
				t.Fatalf("non-callable template range = %#v", diagnostic.Range)
			}
			return
		}
	}
	t.Fatalf("missing template non-callable diagnostic in %#v", diagnostics)
}

func assertCallDiagnostics(t *testing.T, document Document, diagnostics []Diagnostic, want map[string]struct {
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

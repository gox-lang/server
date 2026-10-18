package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticDiagnosticsReportMissingLocalSelectors(t *testing.T) {
	source := `module page
type User struct { Name string }
func (user User) GetName() string { return user.Name }
func Card(user User) gox.Component {
	_ = user.Name
	_ = user.Missing
	return <span>{{ user.GetName() }} {{ user.Unknown }}</span>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	diagnostics = assertAndRemoveUndefinedGoxDiagnostics(t, document, diagnostics, strings.Index(source, "gox.Component"))
	assertUndefinedSelectors(t, document, diagnostics, map[string]int{
		"undefined selector: user.Missing": strings.Index(source, "Missing"),
		"undefined selector: user.Unknown": strings.Index(source, "Unknown"),
	})
}

func TestSemanticDiagnosticsReportUndefinedSelectorQualifiersWithoutImports(t *testing.T) {
	source := `module page
func Card() {
	_ = users.Lookup("George")
	card := <span>{{ users.FormatName("George") }}</span>
	_ = card
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	wantOffsets := map[int]bool{
		strings.Index(source, "users.Lookup"):     true,
		strings.Index(source, "users.FormatName"): true,
	}
	for _, diagnostic := range diagnostics {
		offset, err := document.Offset(diagnostic.Range.Start)
		if err != nil || diagnostic.Code != "undefined-identifier" || diagnostic.Message != "undefined: users" || !wantOffsets[offset] {
			t.Errorf("diagnostic = %#v at %d", diagnostic, offset)
		}
		delete(wantOffsets, offset)
	}
	if len(wantOffsets) != 0 {
		t.Fatalf("missing diagnostic offsets = %#v", wantOffsets)
	}
}

func TestSemanticDiagnosticsReportMissingImportedSelectors(t *testing.T) {
	root := t.TempDir()
	usersSource := `module users
func Lookup(name string) string { return name }
`
	pageSource := `module page
import (
	users "./users.gox"
	"strings"
)
func Build(name string) {
	_ = users.Lookup(name)
	_ = users.Missing(name)
	_ = strings.TrimSpace(name)
	_ = strings.NotReal(name)
}
`
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/app\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	assertUndefinedSelectors(t, document, server.diagnostics(document), map[string]int{
		"undefined selector: users.Missing":   strings.Index(pageSource, "Missing"),
		"undefined selector: strings.NotReal": strings.Index(pageSource, "NotReal"),
	})
}

func TestSemanticDiagnosticsDistinguishUnexportedGoxSelectors(t *testing.T) {
	root := t.TempDir()
	usersSource := `module users
var PublicCount int
var privateCount int
type User struct {
	Name string
	age int
}
func (user User) GetName() string { return user.Name }
func (user User) getAge() int { return user.age }
`
	pageSource := `module page
import users "./users.gox"
func Build(user users.User) {
	_ = users.PublicCount
	_ = users.privateCount
	_ = users.Missing
	_ = user.Name
	_ = user.age
	_ = user.GetName()
	_ = user.getAge()
	_ = user.Unknown
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
		"cannot refer to unexported selector: users.privateCount": {"unexported-selector", strings.Index(pageSource, "privateCount")},
		"undefined selector: users.Missing":                       {"undefined-selector", strings.Index(pageSource, "Missing")},
		"cannot refer to unexported selector: user.age":           {"unexported-selector", strings.Index(pageSource, "user.age") + len("user.")},
		"cannot refer to unexported selector: user.getAge":        {"unexported-selector", strings.Index(pageSource, "user.getAge") + len("user.")},
		"undefined selector: user.Unknown":                        {"undefined-selector", strings.Index(pageSource, "Unknown")},
	}
	if len(diagnostics) != len(want) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		expected, exists := want[diagnostic.Message]
		offset, err := document.Offset(diagnostic.Range.Start)
		if !exists || err != nil || diagnostic.Code != expected.code || offset != expected.offset {
			t.Errorf("diagnostic = %#v at %d", diagnostic, offset)
		}
		delete(want, diagnostic.Message)
	}
	if len(want) != 0 {
		t.Fatalf("missing diagnostics = %#v", want)
	}
}

func TestSemanticDiagnosticsIgnoreSelectorTextOutsideCode(t *testing.T) {
	source := `module page
type User struct { Name string }
func Card(user User) gox.Component {
	// user.Comment
	_ = "user.String"
	return <div data-value="user.Attribute">user.Text {{ user.Name }}</div>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	diagnostics = assertAndRemoveUndefinedGoxDiagnostics(t, document, diagnostics, strings.Index(source, "gox.Component"))
	if len(diagnostics) != 0 {
		t.Fatalf("selector text diagnostics = %#v", diagnostics)
	}
}

func assertAndRemoveUndefinedGoxDiagnostics(t *testing.T, document Document, diagnostics []Diagnostic, offsets ...int) []Diagnostic {
	t.Helper()
	want := make(map[int]bool, len(offsets))
	for _, offset := range offsets {
		want[offset] = true
	}
	filtered := make([]Diagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != "undefined-identifier" || diagnostic.Message != "undefined: gox" {
			filtered = append(filtered, diagnostic)
			continue
		}
		offset, err := document.Offset(diagnostic.Range.Start)
		if err != nil || !want[offset] {
			t.Errorf("unexpected gox diagnostic = %#v at %d", diagnostic, offset)
			continue
		}
		delete(want, offset)
	}
	if len(want) != 0 {
		t.Fatalf("missing gox diagnostic offsets = %#v", want)
	}
	return filtered
}

func assertUndefinedSelectors(t *testing.T, document Document, diagnostics []Diagnostic, want map[string]int) {
	t.Helper()
	if len(diagnostics) != len(want) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		offset, err := document.Offset(diagnostic.Range.Start)
		if err != nil || diagnostic.Code != "undefined-selector" || diagnostic.Source != "gox" || want[diagnostic.Message] != offset {
			t.Errorf("diagnostic = %#v at %d", diagnostic, offset)
		}
		delete(want, diagnostic.Message)
	}
	if len(want) != 0 {
		t.Fatalf("missing diagnostics = %#v", want)
	}
}

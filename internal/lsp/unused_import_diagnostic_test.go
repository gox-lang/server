package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticDiagnosticsReportUnusedGoxImports(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\ntype User struct { Name string }\n")
	tests := []struct {
		name        string
		declaration string
		message     string
	}{
		{name: "implicit alias", declaration: `"./users.gox"`, message: `"./users.gox" imported and not used`},
		{name: "explicit alias", declaration: `u "./users.gox"`, message: `"./users.gox" imported as u and not used`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pagePath := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-")+".gox")
			pageSource := "module page\nimport " + test.declaration + "\nfunc Build() {}\n"
			writeWorkspaceGOX(t, pagePath, pageSource)
			server := NewServer()
			server.indexWorkspaceRoots([]string{root})
			document, ok := server.symbols.Document(FileURI(pagePath))
			if !ok {
				t.Fatal("page document was not indexed")
			}
			assertUnusedImportDiagnostics(t, document, server.diagnostics(document), map[string]int{
				test.message: strings.Index(pageSource, `"./users.gox"`),
			})
		})
	}
}

func TestSemanticDiagnosticsReportUnusedGoImport(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport \"strings\"\nfunc Build() {}\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	assertUnusedImportDiagnostics(t, document, server.diagnostics(document), map[string]int{
		`"strings" imported and not used`: strings.Index(pageSource, `"strings"`),
	})
}

func TestSemanticDiagnosticsCountGoxImportUsesAcrossContexts(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), `module users
type User struct { Name string }
func Lookup(name string) string { return name }
func Card(name string) gox.Component { return <span>{{ name }}</span> }
`)
	tests := []struct {
		name string
		body string
	}{
		{name: "Go selector", body: `_ = users.User{Name: "George"}`},
		{name: "template expression", body: `card := <span>{{ users.Lookup("George") }}</span>; _ = card`},
		{name: "component tag", body: `card := <&users.Card name="George"/>; _ = card`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pagePath := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-")+".gox")
			pageSource := "module page\nimport users \"./users.gox\"\nfunc Build() { " + test.body + " }\n"
			writeWorkspaceGOX(t, pagePath, pageSource)
			server := NewServer()
			server.indexWorkspaceRoots([]string{root})
			document, ok := server.symbols.Document(FileURI(pagePath))
			if !ok {
				t.Fatal("page document was not indexed")
			}
			for _, diagnostic := range server.diagnostics(document) {
				if diagnostic.Code == "unused-import" {
					t.Fatalf("diagnostics = %#v", server.diagnostics(document))
				}
			}
		})
	}
}

func TestSemanticDiagnosticsUseDeclaredModuleNameAsImplicitGoxAlias(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "user.gox"), `module users
type User struct { Name string }
`)
	pagePath := filepath.Join(root, "card.gox")
	pageSource := `module card
import "./user.gox"
func Build(name string) {
	user := users.User{Name: name}
	_ = user
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("card document was not indexed")
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "unused-import" {
			t.Fatalf("diagnostics = %#v", server.diagnostics(document))
		}
	}
}

func TestSemanticDiagnosticsIgnoreBlankAndDotImports(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport (\n\t_ \"./users.gox\"\n\t. \"./users.gox\"\n)\nfunc Build() {}\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "unused-import" {
			t.Fatalf("diagnostics = %#v", server.diagnostics(document))
		}
	}
}

func assertUnusedImportDiagnostics(t *testing.T, document Document, diagnostics []Diagnostic, want map[string]int) {
	t.Helper()
	if len(diagnostics) != len(want) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		offset, err := document.Offset(diagnostic.Range.Start)
		expected, ok := want[diagnostic.Message]
		if !ok || err != nil || diagnostic.Code != "unused-import" || diagnostic.Source != "gox" || offset != expected {
			t.Errorf("diagnostic = %#v at %d", diagnostic, offset)
		}
		delete(want, diagnostic.Message)
	}
	if len(want) != 0 {
		t.Fatalf("missing diagnostics = %#v", want)
	}
}

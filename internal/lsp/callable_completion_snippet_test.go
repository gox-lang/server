package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCallableCompletionsInsertArgumentSnippets(t *testing.T) {
	source := `module page
type User struct{}
func Split(fullname string, separator string) (string, string) { return fullname, separator }
func Ready() bool { return true }
func (user User) Rename(name string, normalize bool) {}
func Build(user User) {
	_, _ = Split("George", " ")
	_ = Ready()
	user.Rename("George", true)
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}

	document.Text = strings.Replace(source, `_, _ = Split("George", " ")`, "Spl", 1)
	assertCallableSnippet(t, completionItemsAtEnd(t, server, document, "Spl"), "Split", "Split(${1:fullname}, ${2:separator})$0")

	document.Text = strings.Replace(source, "_ = Ready()", "Rea", 1)
	assertCallableSnippet(t, completionItemsAtEnd(t, server, document, "Rea"), "Ready", "Ready()$0")

	document.Text = strings.Replace(source, `user.Rename("George", true)`, "user.Re", 1)
	assertCallableSnippet(t, completionItemsAtEnd(t, server, document, "user.Re"), "Rename", "Rename(${1:name}, ${2:normalize})$0")
}

func TestCallableCompletionsCoverGOXModulesAndGoPackages(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), `module users
func Lookup(id int, strict bool) (string, error) { return "", nil }
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import (
	users "./users.gox"
	"strings"
)
func Build() {
	_, _ = users.Lookup(1, true)
	_ = strings.Split("George Milo", " ")
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	document.Text = strings.Replace(pageSource, "_, _ = users.Lookup(1, true)", "users.Lo", 1)
	assertCallableSnippet(t, completionItemsAtEnd(t, server, document, "users.Lo"), "Lookup", "Lookup(${1:id}, ${2:strict})$0")

	document.Text = strings.Replace(pageSource, `_ = strings.Split("George Milo", " ")`, "strings.Sp", 1)
	assertCallableSnippet(t, completionItemsAtEnd(t, server, document, "strings.Sp"), "Split", "Split(${1:s}, ${2:sep})$0")
}

func TestCallableCompletionDoesNotDuplicateExistingParenthesis(t *testing.T) {
	source := `module page
func Split(value string) string { return value }
func Build() { _ = Split("George") }
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	document.Text = strings.Replace(source, `_ = Split("George")`, "Spl()", 1)
	offset := strings.LastIndex(document.Text, "Spl") + len("Spl")
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	item := completionItemNamed(t, server.completions(document, position).Items, "Split")
	if item.InsertText != "Split" || item.InsertTextFormat != 0 {
		t.Fatalf("completion before existing parenthesis = %#v", item)
	}
}

func TestCallableCompletionKeepsComponentsOutOfGoCalls(t *testing.T) {
	source := `module page
func Card(name string) gox.Component { return <span>{{ name }}</span> }
func Build() { }
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	document.Text = strings.Replace(source, "func Build() { }", "func Build() { Car }", 1)
	items := completionItemsAtEnd(t, server, document, "Car")
	assertCompletionMissing(t, items, "Card")
}

func assertCallableSnippet(t *testing.T, items []CompletionItem, label, snippet string) {
	t.Helper()
	item := completionItemNamed(t, items, label)
	if item.InsertText != snippet || item.InsertTextFormat != 2 {
		t.Fatalf("%s completion = %#v, want snippet %q", label, item, snippet)
	}
}

func completionItemNamed(t *testing.T, items []CompletionItem, label string) CompletionItem {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("completion %q not found in %#v", label, items)
	return CompletionItem{}
}

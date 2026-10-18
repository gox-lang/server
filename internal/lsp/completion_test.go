package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionProvidesExportedGoxModuleMembers(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), `module users
type User struct { Name string }
var DefaultName string
const Limit = 10
func SplitName(fullname string) (string, string) { return fullname, fullname }
func privateFunction() {}
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport \"users.gox\"\nfunc Build() { var _ users.User }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	document.Text = strings.Replace(pageSource, "users.User", "users.", 1)
	items := completionItemsAtEnd(t, server, document, "users.")
	assertCompletionLabels(t, items, "DefaultName", "Limit", "SplitName", "User")
	assertCompletionMissing(t, items, "privateFunction")

	document.Text = strings.Replace(pageSource, "users.User", "users.Sp", 1)
	filtered := completionItemsAtEnd(t, server, document, "users.Sp")
	assertCompletionLabels(t, filtered, "SplitName")
	if filtered[0].Detail != "func SplitName(fullname string) (string, string)" {
		t.Fatalf("SplitName completion = %#v", filtered[0])
	}
}

func TestCompletionProvidesGoPackageMembersWithExplicitAlias(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "library", "api.go"), `package api
type User struct{ Name string }
const Limit = 10
func SplitName(fullname string) (string, string) { return fullname, fullname }
func privateFunction() {}
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport library \"example/library\"\nfunc Build() { var _ library.User }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	document.Text = strings.Replace(pageSource, "library.User", "library.Sp", 1)
	items := completionItemsAtEnd(t, server, document, "library.Sp")
	assertCompletionLabels(t, items, "SplitName")
	if items[0].Kind != 3 || items[0].Detail != "func SplitName(fullname string) (string, string)" {
		t.Fatalf("Go completion = %#v", items[0])
	}
}

func TestCompletionUsesDeclaredPackageNameForImplicitLocalImport(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), `package register
func Create() string { return "created" }
`)
	pagePath := filepath.Join(root, "pages", "index.gox")
	pageSource := `module page
import "gox-editor-test/billing"
func Build() { _ = register.Cr }
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	items := completionItemsAtEnd(t, server, document, "register.Cr")
	assertCompletionLabels(t, items, "Create")
	assertCompletionMissing(t, items, "billing")
}

func TestCompletionProvidesLocalAndTemplateValueMembersByType(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
type User struct { Name string; age int }
func (user User) GetName() string { return user.Name }
func (user *User) setName(name string) { user.Name = name }
func Card() string {
	user := User{Name: "George"}
	return <span>{{ user.Name }}</span>
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	document.Text = strings.Replace(pageSource, "user.Name }}</span>", "user. }}</span>", 1)
	items := completionItemsAtEnd(t, server, document, "user.")
	assertCompletionLabels(t, items, "GetName", "Name", "age", "setName")
	if items[0].Kind != 2 || items[1].Kind != 5 {
		t.Fatalf("local type member kinds = %#v", items)
	}
}

func TestCompletionRepairsMultipleIncompleteSelectorsFromLiveDocument(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	document := Document{
		URI:  FileURI(pagePath),
		Path: pagePath,
		Text: `module page
type User struct { Name string; age int }
func (user User) GetName() string { return user.Name }
func Card() string {
	user := User{Name: "George"}
	user.
	return <span>{{ user. }}</span>
}
`,
	}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err == nil {
		t.Fatal("incomplete selector unexpectedly produced a valid index")
	}
	firstOffset := strings.Index(document.Text, "\tuser.\n") + len("\tuser.")
	firstPosition, err := document.Position(firstOffset)
	if err != nil {
		t.Fatal(err)
	}
	assertCompletionLabels(t, server.completions(document, firstPosition).Items, "GetName", "Name", "age")
	assertCompletionLabels(t, completionItemsAtEnd(t, server, document, "user."), "GetName", "Name", "age")
}

func TestCompletionFiltersImportedGoxValueMembersByVisibility(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), `module users
type User struct { Name string; age int }
func (user User) GetName() string { return user.Name }
func (user *User) SetName(name string) { user.Name = name }
func (user *User) setAge(age int) { user.age = age }
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport \"users.gox\"\nfunc Build() { var user users.User; _ = user.Name }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	document.Text = strings.Replace(pageSource, "user.Name", "user.", 1)
	items := completionItemsAtEnd(t, server, document, "user.")
	assertCompletionLabels(t, items, "GetName", "Name", "SetName")
	assertCompletionMissing(t, items, "age")
	assertCompletionMissing(t, items, "setAge")
}

func TestCompletionProvidesGoTypeFieldsAndPointerMethods(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "library", "api.go"), `package api
type Context struct { Name string; age int }
func (ctx Context) Read() string { return ctx.Name }
func (ctx *Context) Write(name string) { ctx.Name = name }
func (ctx *Context) private() {}
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport library \"example/library\"\nfunc Build(ctx *library.Context) { _ = ctx.Name }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	document.Text = strings.Replace(pageSource, "ctx.Name", "ctx.", 1)
	items := completionItemsAtEnd(t, server, document, "ctx.")
	assertCompletionLabels(t, items, "Name", "Read", "Write")
	assertCompletionMissing(t, items, "age")
	assertCompletionMissing(t, items, "private")
}

func completionItemsAtEnd(t *testing.T, server *Server, document Document, marker string) []CompletionItem {
	t.Helper()
	offset := strings.LastIndex(document.Text, marker)
	if offset < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	position, err := document.Position(offset + len(marker))
	if err != nil {
		t.Fatal(err)
	}
	return server.completions(document, position).Items
}

func assertCompletionLabels(t *testing.T, items []CompletionItem, want ...string) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("completion items = %#v, want %v", items, want)
	}
	for index := range want {
		if items[index].Label != want[index] {
			t.Fatalf("completion items = %#v, want %v", items, want)
		}
	}
}

func assertCompletionMissing(t *testing.T, items []CompletionItem, label string) {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			t.Fatalf("completion unexpectedly contains %q: %#v", label, items)
		}
	}
}

func TestCompletionProvidesFunctionLiteralParameterMembers(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
type User struct { Name string }
func (user User) GetName() string { return user.Name }
func Build() {
	callback := func(user User) {
		_ = user.Name
	}
	_ = callback
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	document.Text = strings.Replace(pageSource, "user.Name", "user.", 1)
	items := completionItemsAtEnd(t, server, document, "user.")
	assertCompletionLabels(t, items, "GetName", "Name")
}

func TestFunctionLiteralParameterDoesNotEscapeItsScope(t *testing.T) {
	source := `module page
type User struct { Name string }
func Build() {
	callback := func(user User) { _ = user.Name }
	_ = callback
	_ = user.Name
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	wantOffset := strings.LastIndex(source, "user.Name")
	found := false
	for _, diagnostic := range diagnostics {
		offset, err := document.Offset(diagnostic.Range.Start)
		if err == nil && diagnostic.Code == "undefined-identifier" && diagnostic.Message == "undefined: user" && offset == wantOffset {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing undefined user diagnostic outside function literal: %#v", diagnostics)
	}
}

func TestCompletionProvidesGlobalCompositeFunctionLiteralParameterMembers(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
type User struct { Name string }
func (user *User) GetName() string { return user.Name }
var Config = struct {
	OnCreate func(*User)
}{
	OnCreate: func(user *User) {
		_ = user.Name
	},
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	document.Text = strings.Replace(pageSource, "user.Name\n\t},", "user.\n\t},", 1)
	items := completionItemsAtEnd(t, server, document, "user.")
	assertCompletionLabels(t, items, "GetName", "Name")
}

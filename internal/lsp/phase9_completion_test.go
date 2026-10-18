package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase9SemanticQualifiedCompletionOwnsImportedGoxModuleAndValue(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), `module users
type User struct { Name string; age int }
func Lookup(id int) User { return User{} }
func hidden() string { return "" }
func (user User) GetName() string { return user.Name }
func (user *User) SetName(name string) { user.Name = name }
func (user *User) setAge(age int) { user.age = age }
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import "users.gox"
func Build() {
	var user users.User
	_ = users.Lookup(1)
	_ = user.Name
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})

	packageDocument := Document{URI: FileURI(pagePath), Path: pagePath, Text: strings.Replace(pageSource, "users.Lookup(1)", "users.", 1)}
	packageOffset := strings.Index(packageDocument.Text, "users.") + len("users.")
	packageItems, handled := server.semanticCompletionItems(packageDocument, packageOffset, "")
	if !handled {
		t.Fatal("semantic completion did not own imported GOX package selector")
	}
	assertCompletionContainsOnlyPublicOrdinary(t, packageItems, []string{"Lookup", "User"}, []string{"hidden"})

	valueDocument := Document{URI: FileURI(pagePath), Path: pagePath, Text: strings.Replace(pageSource, "user.Name", "user.", 1)}
	valueOffset := strings.LastIndex(valueDocument.Text, "user.") + len("user.")
	valueItems, handled := server.semanticCompletionItems(valueDocument, valueOffset, "")
	if !handled {
		t.Fatal("semantic completion did not own imported GOX value selector")
	}
	assertCompletionContainsOnlyPublicOrdinary(t, valueItems, []string{"GetName", "Name", "SetName"}, []string{"age", "setAge"})
}

func TestPhase9SemanticPackageCompletionPreservesSourceAliasInTypes(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "library", "api.go"), `package api
type User struct{ Name string }
func Create() User { return User{} }
`)
	path := filepath.Join(root, "page.gox")
	source := `module page
import library "example/library"
func Build() { _ = library.Create() }
`
	writeWorkspaceGOX(t, path, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(path), Path: path, Text: strings.Replace(source, "library.Create()", "library.Cr", 1)}
	offset := strings.Index(document.Text, "library.Cr") + len("library.Cr")
	items, handled := server.semanticCompletionItems(document, offset, "Cr")
	if !handled {
		t.Fatal("semantic completion did not own imported Go package selector")
	}
	item := completionItemNamed(t, items, "Create")
	if len(item.CandidateResults) != 1 || item.CandidateResults[0] != "library.User" {
		t.Fatalf("semantic completion result types = %#v, want library.User", item)
	}
}

func TestPhase9SemanticLexicalCompletionOwnsInferredLocalType(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() {
	value := strings.NewReplacer("a", "b")
	val
}
`
	writeWorkspaceGOX(t, path, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(path), Path: path, Text: source}
	offset := strings.LastIndex(source, "val") + len("val")
	module, semanticOffset, live := server.completionModule(document, offset)
	if !live {
		t.Fatal("completion module is not semantically live")
	}
	items, handled := server.semanticVisibleCompletionItems(document, module, semanticOffset, "val")
	if !handled {
		t.Fatal("semantic lexical completion did not own valid Go scope")
	}
	item := completionItemNamed(t, items, "value")
	if item.CandidateType != "*strings.Replacer" {
		t.Fatalf("value completion = %#v, want *strings.Replacer", item)
	}
}

func assertCompletionContainsOnlyPublicOrdinary(t *testing.T, items []CompletionItem, present, absent []string) {
	t.Helper()
	for _, label := range present {
		requireCompletionItem(t, items, label)
	}
	for _, label := range absent {
		assertCompletionMissing(t, items, label)
	}
}

func TestPhase9SemanticLexicalCompletionUsesTemplateDirectiveScopes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "page.gox")
	source := `module page
type User struct { Name string }
func Card() string {
	users := []User{{Name: "George"}}
	return <section>
		<:for index user range={{ users }}>
			<span>{{ use }}</span>
		</:for>
		<span>{{ use }}</span>
	</section>
}
`
	writeWorkspaceGOX(t, path, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(path), Path: path, Text: source}

	first := strings.Index(source, "{{ use") + len("{{ use")
	module, semanticOffset, live := server.completionModule(document, first)
	if !live {
		t.Fatal("template completion module is not semantically live")
	}
	inside, handled := server.semanticVisibleCompletionItems(document, module, semanticOffset, "use")
	if !handled {
		t.Fatal("semantic completion did not own template loop scope")
	}
	requireCompletionItem(t, inside, "user")
	requireCompletionItem(t, inside, "users")

	secondStart := strings.LastIndex(source, "{{ use")
	second := secondStart + len("{{ use")
	module, semanticOffset, live = server.completionModule(document, second)
	if !live {
		t.Fatal("outer template completion module is not semantically live")
	}
	outside, handled := server.semanticVisibleCompletionItems(document, module, semanticOffset, "use")
	if !handled {
		t.Fatal("semantic completion did not own outer template scope")
	}
	requireCompletionItem(t, outside, "users")
	assertCompletionMissing(t, outside, "user")
}

func TestPhase9SemanticCompletionKeepsGoxSDKInternalMembersHidden(t *testing.T) {
	root, _, _ := goxSDKPublicAPIFixture(t)
	path := filepath.Join(root, "context.gox")
	source := `module contextpage
import gox "github.com/gox-lang/gox"
func Build(ctx *gox.Context) {
	_ = ctx.Request
}
`
	writeWorkspaceGOX(t, path, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(path), Path: path, Text: strings.Replace(source, "ctx.Request", "ctx.", 1)}
	offset := strings.Index(document.Text, "ctx.") + len("ctx.")
	items, handled := server.semanticCompletionItems(document, offset, "")
	if !handled {
		t.Fatal("semantic completion did not own GOX SDK value selector")
	}
	requireCompletionItem(t, items, "Request")
	assertCompletionMissing(t, items, "RuntimeField")
	assertCompletionMissing(t, items, "RuntimeMethod")
}

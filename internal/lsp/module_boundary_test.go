package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefinitionsRespectGOXModuleBoundaries(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	cardPath := filepath.Join(root, "cards", "card.gox")
	usersSource := `module users
type User struct {
	Name string
	age int
}
func (user User) GetName() string { return user.Name }
func (user *User) SetName(name string) { user.Name = name }
`
	cardSource := `module card
import users "../users.gox"
func Build(name string) {
	var user users.User
	_ = user.Name
	_ = user.GetName()
	var invalid User
	_ = invalid
}
`
	writeWorkspaceGOX(t, usersPath, usersSource)
	writeWorkspaceGOX(t, cardPath, cardSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(cardPath))
	if !ok {
		t.Fatal("card document was not indexed")
	}

	assertDefinitionURIAndOffset(t, server, document,
		strings.Index(cardSource, "users.User")+len("users.")+1,
		FileURI(usersPath), usersSource, strings.Index(usersSource, "User struct"))
	assertDefinitionURIAndOffset(t, server, document,
		strings.Index(cardSource, "user.Name")+len("user.")+1,
		FileURI(usersPath), usersSource, strings.Index(usersSource, "Name string"))
	assertDefinitionURIAndOffset(t, server, document,
		strings.Index(cardSource, "user.GetName")+len("user.")+1,
		FileURI(usersPath), usersSource, strings.Index(usersSource, "GetName"))

	fieldHover := hoverAtOffset(t, server, document, strings.Index(cardSource, "user.Name")+len("user.")+1)
	if fieldHover == nil || !strings.Contains(fieldHover.Contents.Value, "field Name string") {
		t.Fatalf("field hover = %#v", fieldHover)
	}
	methodHover := hoverAtOffset(t, server, document, strings.Index(cardSource, "user.GetName")+len("user.")+1)
	if methodHover == nil || !strings.Contains(methodHover.Contents.Value, "func (user User) GetName() string") {
		t.Fatalf("method hover = %#v", methodHover)
	}

	bareUser := strings.LastIndex(cardSource, "User") + 1
	if locations := definitionLocationsAtOffset(t, server, document, bareUser); len(locations) != 0 {
		t.Fatalf("bare cross-module User locations = %#v", locations)
	}
}

func TestImportedValueDoesNotExposePrivateMembers(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	cardPath := filepath.Join(root, "card.gox")
	usersSource := "module users\ntype User struct { age int }\n"
	cardSource := "module card\nimport users \"./users.gox\"\nfunc Build() { var user users.User; _ = user.age }\n"
	writeWorkspaceGOX(t, usersPath, usersSource)
	writeWorkspaceGOX(t, cardPath, cardSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(cardPath))
	if !ok {
		t.Fatal("card document was not indexed")
	}
	offset := strings.LastIndex(cardSource, "age") + 1
	if locations := definitionLocationsAtOffset(t, server, document, offset); len(locations) != 0 {
		t.Fatalf("private field locations = %#v", locations)
	}
	if hover := hoverAtOffset(t, server, document, offset); hover != nil {
		t.Fatalf("private field hover = %#v", hover)
	}
}

func TestAllExportableSymbolsRequireModuleQualification(t *testing.T) {
	root := t.TempDir()
	libraryPath := filepath.Join(root, "library.gox")
	pagePath := filepath.Join(root, "page.gox")
	librarySource := `module library
const Limit = 10
var DefaultName = "George"
type User struct { Name string }
type Reader interface { Read() string }
func Build() string { return DefaultName }
func Card() gox.Component { return <article>George</article> }
`
	pageSource := `module page
import library "./library.gox"
func Render() gox.Component {
	_ = Limit
	_ = DefaultName
	var _ User
	var _ Reader
	_ = Build
	return <&Card/>
}
`
	writeWorkspaceGOX(t, libraryPath, librarySource)
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	for _, name := range []string{"Limit", "DefaultName", "User", "Reader", "Build", "Card"} {
		offset := strings.LastIndex(pageSource, name) + 1
		if locations := definitionLocationsAtOffset(t, server, document, offset); len(locations) != 0 {
			t.Errorf("unqualified %s locations = %#v", name, locations)
		}
		if hover := hoverAtOffset(t, server, document, offset); hover != nil {
			t.Errorf("unqualified %s hover = %#v", name, hover)
		}
	}
}

func TestAllExportableSymbolsResolveThroughModuleQualification(t *testing.T) {
	root := t.TempDir()
	libraryPath := filepath.Join(root, "library.gox")
	pagePath := filepath.Join(root, "page.gox")
	librarySource := `module library
const Limit = 10
var DefaultName = "George"
type User struct { Name string }
type Reader interface { Read() string }
func Build() string { return DefaultName }
func Card() gox.Component { return <article>George</article> }
`
	pageSource := `module page
import library "./library.gox"
func Render() gox.Component {
	_ = library.Limit
	_ = library.DefaultName
	var _ library.User
	var _ library.Reader
	_ = library.Build
	return <&library.Card/>
}
`
	writeWorkspaceGOX(t, libraryPath, librarySource)
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	for _, name := range []string{"Limit", "DefaultName", "User", "Reader", "Build", "Card"} {
		useOffset := strings.LastIndex(pageSource, name) + 1
		targetOffset := strings.Index(librarySource, name)
		assertDefinitionURIAndOffset(t, server, document, useOffset, FileURI(libraryPath), librarySource, targetOffset)
		if hover := hoverAtOffset(t, server, document, useOffset); hover == nil {
			t.Errorf("qualified %s hover is nil", name)
		}
	}
}

func TestModuleQualificationDoesNotExposePrivateTopLevelSymbols(t *testing.T) {
	root := t.TempDir()
	libraryPath := filepath.Join(root, "library.gox")
	pagePath := filepath.Join(root, "page.gox")
	librarySource := `module library
const limit = 10
var defaultName = "George"
type user struct { Name string }
type reader interface { Read() string }
func build() string { return defaultName }
func card() gox.Component { return <article>George</article> }
`
	pageSource := `module page
import library "./library.gox"
func Render() gox.Component {
	_ = library.limit
	_ = library.defaultName
	var _ library.user
	var _ library.reader
	_ = library.build
	return <&library.card/>
}
`
	writeWorkspaceGOX(t, libraryPath, librarySource)
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	for _, name := range []string{"limit", "defaultName", "user", "reader", "build", "card"} {
		offset := strings.LastIndex(pageSource, name) + 1
		if locations := definitionLocationsAtOffset(t, server, document, offset); len(locations) != 0 {
			t.Errorf("private qualified %s locations = %#v", name, locations)
		}
		if hover := hoverAtOffset(t, server, document, offset); hover != nil {
			t.Errorf("private qualified %s hover = %#v", name, hover)
		}
	}
}

func assertDefinitionURIAndOffset(t *testing.T, server *Server, document Document, useOffset int, targetURI, targetSource string, targetOffset int) {
	t.Helper()
	locations := definitionLocationsAtOffset(t, server, document, useOffset)
	target := Document{URI: targetURI, Text: targetSource}
	want, err := target.Position(targetOffset)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 || locations[0].URI != targetURI || locations[0].Range.Start != want {
		t.Fatalf("definition at %d = %#v, want %s:%#v", useOffset, locations, targetURI, want)
	}
}

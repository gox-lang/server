package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefinitionNavigatesToNearestLocalDeclaration(t *testing.T) {
	source := `module page
func Render() gox.Component {
	name := "George"
	if true {
		name := "Milo"
		_ = name
	}
	_ = name
	return <span>{{ name }}</span>
}`
	document := Document{URI: "file:///project/pages/index.gox", Path: "/project/pages/index.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	innerUse := strings.Index(source, "_ = name") + len("_ = ") + 1
	outerUse := strings.LastIndex(source, "_ = name") + len("_ = ") + 1
	assertDefinitionOffset(t, server, document, innerUse, strings.LastIndex(source[:innerUse], "name :="))
	assertDefinitionOffset(t, server, document, outerUse, strings.Index(source, "name :="))
}

func TestDefinitionNavigatesToTemplateLoopBinding(t *testing.T) {
	source := `module page
func Render() gox.Component {
	users := []string{"George", "Milo"}
	return <:for _ user range={{ users }}><span>{{ user }}</span></:for>
}`
	document := Document{URI: "file:///project/pages/index.gox", Path: "/project/pages/index.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	assertDefinitionOffset(t, server, document, strings.Index(source, "{{ users }}")+3, strings.Index(source, "users :="))
	assertDefinitionOffset(t, server, document, strings.Index(source, "{{ user }}")+3, strings.Index(source, "user range"))
}

func TestDefinitionResolvesImportedModuleQualifierAndMember(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	otherPath := filepath.Join(root, "other.gox")
	pagePath := filepath.Join(root, "pages", "index.gox")
	usersSource := "module users\ntype User struct { Name string }\n"
	otherSource := "module other\ntype User struct { ID string }\n"
	pageSource := "module page\nimport users \"../users.gox\"\nfunc Build() { var value users.User; _ = value }\n"
	writeWorkspaceGOX(t, usersPath, usersSource)
	writeWorkspaceGOX(t, otherPath, otherSource)
	writeWorkspaceGOX(t, pagePath, pageSource)

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	qualifierOffset := strings.LastIndex(pageSource, "users.User") + 2
	memberOffset := strings.LastIndex(pageSource, "users.User") + len("users.") + 2
	qualifierLocations := definitionLocationsAtOffset(t, server, document, qualifierOffset)
	memberLocations := definitionLocationsAtOffset(t, server, document, memberOffset)
	if len(qualifierLocations) != 1 || qualifierLocations[0].URI != FileURI(usersPath) {
		t.Fatalf("qualifier locations = %#v", qualifierLocations)
	}
	if len(memberLocations) != 1 || memberLocations[0].URI != FileURI(usersPath) {
		t.Fatalf("member locations = %#v", memberLocations)
	}
}

func assertDefinitionOffset(t *testing.T, server *Server, document Document, useOffset, declarationOffset int) {
	t.Helper()
	locations := definitionLocationsAtOffset(t, server, document, useOffset)
	want, err := document.Position(declarationOffset)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 || locations[0].URI != document.URI || locations[0].Range.Start != want {
		t.Fatalf("definition at %d = %#v, want %s:%#v", useOffset, locations, document.URI, want)
	}
}

func definitionLocationsAtOffset(t *testing.T, server *Server, document Document, offset int) []Location {
	t.Helper()
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	return server.definitionLocations(document, position)
}

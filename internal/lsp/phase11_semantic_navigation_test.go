package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase11SemanticIdentityOwnsLocalReferencesAcrossGoAndTemplate(t *testing.T) {
	source := `module page
func Build() string {
	name := "George"
	_ = name
	if true { name := "Milo"; _ = name }
	return <span>{{ name }}</span>
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	position := referencePosition(t, document, strings.Index(source, `name := "George"`))
	locations, handled := server.semanticReferences(document, position, true)
	if !handled {
		t.Fatal("semantic references did not own local symbol")
	}
	assertReferenceOffsets(t, document, locations,
		strings.Index(source, `name := "George"`),
		strings.Index(source, "_ = name")+len("_ = "),
		strings.LastIndex(source, "name }}"),
	)
}

func TestPhase11SemanticIdentityOwnsImportedGoxDefinitionAndReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	usersSource := `module users
func Lookup(id int) string { return "George" }
`
	writeWorkspaceGOX(t, usersPath, usersSource)
	firstPath := filepath.Join(root, "first.gox")
	firstSource := `module first
import users "./users.gox"
func Build() { _ = users.Lookup(1) }
`
	writeWorkspaceGOX(t, firstPath, firstSource)
	secondPath := filepath.Join(root, "second.gox")
	secondSource := `module second
import people "./users.gox"
func Build() { _ = people.Lookup(2) }
`
	writeWorkspaceGOX(t, secondPath, secondSource)

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(firstPath))
	if !ok {
		t.Fatal("first document was not indexed")
	}
	position := referencePosition(t, document, strings.Index(firstSource, "Lookup"))

	definitions, handled := server.semanticDefinitionLocations(document, position)
	if !handled || len(definitions) != 1 || definitions[0].URI != FileURI(usersPath) {
		t.Fatalf("semantic definition = %#v, handled=%v", definitions, handled)
	}
	wantDefinition := referencePosition(t, Document{URI: FileURI(usersPath), Path: usersPath, Text: usersSource}, strings.Index(usersSource, "Lookup"))
	if definitions[0].Range.Start != wantDefinition {
		t.Fatalf("semantic definition = %#v, want offset %d", definitions[0], strings.Index(usersSource, "Lookup"))
	}

	locations, handled := server.semanticReferences(document, position, true)
	if !handled || len(locations) != 3 {
		t.Fatalf("semantic references = %#v, handled=%v", locations, handled)
	}
	if locations[0].URI != FileURI(firstPath) || locations[1].URI != FileURI(secondPath) || locations[2].URI != FileURI(usersPath) {
		t.Fatalf("semantic reference ordering/identity = %#v", locations)
	}
}

func TestPhase11SemanticIdentityDistinguishesSameNamedFields(t *testing.T) {
	source := `module page
type User struct { Name string }
type Profile struct { Name string }
func Build(user User, profile Profile) {
	_ = user.Name
	_ = profile.Name
	user.Name = "George"
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	declaration := strings.Index(source, "Name string")
	locations, handled := server.semanticReferences(document, referencePosition(t, document, declaration), true)
	if !handled {
		t.Fatal("semantic references did not own field")
	}
	assertReferenceOffsets(t, document, locations,
		declaration,
		strings.Index(source, "user.Name")+len("user."),
		strings.LastIndex(source, "user.Name")+len("user."),
	)
}

func TestPhase11SemanticIdentityTracksGoPackageObjectAcrossWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(root, "first.gox")
	firstSource := `module first
import "strings"
func Build() { _ = strings.Split("George Milo", " ") }
`
	writeWorkspaceGOX(t, firstPath, firstSource)
	secondPath := filepath.Join(root, "second.gox")
	secondSource := `module second
import text "strings"
func Build() { _ = text.Split("Milo", " ") }
`
	writeWorkspaceGOX(t, secondPath, secondSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(firstPath))
	if !ok {
		t.Fatal("first document was not indexed")
	}
	position := referencePosition(t, document, strings.Index(firstSource, "Split"))
	locations, handled := server.semanticReferences(document, position, true)
	if !handled || len(locations) != 2 || locations[0].URI != FileURI(firstPath) || locations[1].URI != FileURI(secondPath) {
		t.Fatalf("semantic strings.Split references = %#v, handled=%v", locations, handled)
	}
	renamable, handled := server.semanticRenamableAt(document, position)
	if !handled || renamable {
		t.Fatalf("semantic rename decision for strings.Split = renamable %v handled %v", renamable, handled)
	}
}

func TestPhase11SemanticRenameOwnsImportedGoxSymbol(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	writeWorkspaceGOX(t, usersPath, "module users\nfunc Lookup(id int) string { return \"George\" }\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport users \"./users.gox\"\nfunc Build() { _ = users.Lookup(1) }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	position := referencePosition(t, document, strings.Index(pageSource, "Lookup"))
	renamable, handled := server.semanticRenamableAt(document, position)
	if !handled || !renamable {
		t.Fatalf("semantic rename decision = renamable %v handled %v", renamable, handled)
	}
}

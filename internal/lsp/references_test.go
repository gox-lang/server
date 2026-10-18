package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferencesRespectLocalScopesAndShadowing(t *testing.T) {
	source := `module page
func Build() string {
	name := "George"
	_ = name
	if true {
		name := "Milo"
		_ = name
	}
	return <span>{{ name }}</span>
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	position := referencePosition(t, document, strings.Index(source, `name := "George"`))
	locations := server.references(document, position, true)
	assertReferenceOffsets(t, document, locations,
		strings.Index(source, `name := "George"`),
		strings.Index(source, "_ = name")+len("_ = "),
		strings.LastIndex(source, "name }}"),
	)
	withoutDeclaration := server.references(document, position, false)
	assertReferenceOffsets(t, document, withoutDeclaration,
		strings.Index(source, "_ = name")+len("_ = "),
		strings.LastIndex(source, "name }}"),
	)
}

func TestReferencesDistinguishFieldsWithTheSameName(t *testing.T) {
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
	locations := server.references(document, referencePosition(t, document, declaration), true)
	assertReferenceOffsets(t, document, locations,
		declaration,
		strings.Index(source, "user.Name")+len("user."),
		strings.LastIndex(source, "user.Name")+len("user."),
	)
}

func TestReferencesCrossGOXModulesAndTemplateComponents(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	cardPath := filepath.Join(root, "card.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, usersPath, `module users
func Lookup(id int) string { return "George" }
`)
	writeWorkspaceGOX(t, cardPath, `module cards
func Card(name string) gox.Component { return <span>{{ name }}</span> }
`)
	pageSource := `module page
import (
	users "./users.gox"
	cards "./card.gox"
)
client Menu react { src: "menu.jsx" }
func Build() gox.Component {
	_ = users.Lookup(1)
	_ = users.Lookup(2)
	return <main><&cards.Card name={{ users.Lookup(3) }}/><@Menu/></main>
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	lookupPosition := referencePosition(t, document, strings.Index(pageSource, "Lookup"))
	lookupReferences := server.references(document, lookupPosition, true)
	if len(lookupReferences) != 4 {
		t.Fatalf("Lookup references = %#v, want declaration and three uses", lookupReferences)
	}
	aliasDeclaration := strings.Index(pageSource, `users "./users.gox"`)
	aliasReferences := server.references(document, referencePosition(t, document, aliasDeclaration), true)
	if len(aliasReferences) != 5 {
		t.Fatalf("users alias references = %#v, want module declaration, import alias and three uses", aliasReferences)
	}

	cardPosition := referencePosition(t, document, strings.Index(pageSource, "Card"))
	cardReferences := server.references(document, cardPosition, true)
	if len(cardReferences) != 2 || cardReferences[0].URI != FileURI(cardPath) || cardReferences[1].URI != FileURI(pagePath) {
		t.Fatalf("Card references = %#v", cardReferences)
	}

	menuDeclaration := strings.Index(pageSource, "Menu react")
	menuReferences := server.references(document, referencePosition(t, document, menuDeclaration), true)
	assertReferenceOffsets(t, document, menuReferences, menuDeclaration, strings.Index(pageSource, "<@Menu")+len("<@"))
}

func TestReferencesTrackTemplateLoopBindingsWithoutLeakingScope(t *testing.T) {
	source := `module page
func Card(users []string) gox.Component {
	return <section>
		<:for index user range={{ users }}>
			<span>{{ index }} {{ user }}</span>
		</:for>
		<span>{{ user }}</span>
	</section>
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	declaration := strings.Index(source, "index user")
	locations := server.references(document, referencePosition(t, document, declaration), true)
	assertReferenceOffsets(t, document, locations, declaration, strings.Index(source, "index }}"))
	userDeclaration := declaration + len("index ")
	userLocations := server.references(document, referencePosition(t, document, userDeclaration), true)
	assertReferenceOffsets(t, document, userLocations, userDeclaration, strings.Index(source, "user }}</span>"))
}

func TestReferencesFindGoPackageUsesAcrossWorkspace(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	firstPath := filepath.Join(root, "first.gox")
	secondPath := filepath.Join(root, "second.gox")
	firstSource := `module first
import "strings"
func Build() { _ = strings.Split("George Milo", " ") }
`
	secondSource := `module second
import text "strings"
func Build() { _ = text.Split("Milo", " ") }
`
	writeWorkspaceGOX(t, firstPath, firstSource)
	writeWorkspaceGOX(t, secondPath, secondSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(firstPath))
	if !ok {
		t.Fatal("first document was not indexed")
	}
	locations := server.references(document, referencePosition(t, document, strings.Index(firstSource, "Split")), true)
	if len(locations) != 2 || locations[0].URI != FileURI(firstPath) || locations[1].URI != FileURI(secondPath) {
		t.Fatalf("strings.Split references = %#v", locations)
	}
}

func TestServerReturnsReferencesOverLSP(t *testing.T) {
	uri := "file:///project/page.gox"
	source := "module page\nfunc Join(value string) string { return value }\nfunc Build() { _ = Join(\"George\") }\n"
	document := Document{URI: uri, Path: "/project/page.gox", Text: source}
	position := referencePosition(t, document, strings.LastIndex(source, "Join"))
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/references","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":` + referenceInt(position.Line) + `,"character":` + referenceInt(position.Character) + `},"context":{"includeDeclaration":true}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	for _, response := range decodeFramedResponses(t, output.String()) {
		if string(response.ID) != "2" {
			continue
		}
		var locations []Location
		if err := json.Unmarshal(response.Result, &locations); err != nil {
			t.Fatal(err)
		}
		if len(locations) != 2 {
			t.Fatalf("references response = %#v", locations)
		}
		return
	}
	t.Fatalf("references response not found: %s", output.String())
}

func referencePosition(t *testing.T, document Document, offset int) Position {
	t.Helper()
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	return position
}

func assertReferenceOffsets(t *testing.T, document Document, locations []Location, offsets ...int) {
	t.Helper()
	if len(locations) != len(offsets) {
		t.Fatalf("references = %#v, want offsets %v", locations, offsets)
	}
	for index, offset := range offsets {
		position := referencePosition(t, document, offset)
		if locations[index].URI != document.URI || locations[index].Range.Start != position {
			t.Fatalf("reference %d = %#v, want %s at %#v", index, locations[index], document.URI, position)
		}
	}
}

func referenceInt(value int) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}

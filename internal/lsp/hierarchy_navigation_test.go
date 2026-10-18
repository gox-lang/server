package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCallHierarchyFindsIncomingOutgoingAndTemplateCalls(t *testing.T) {
	source := `module calls
func Leaf() string { return "leaf" }
func Middle() string { return Leaf() }
func Card() gox.Component { return <span /> }
func Page() gox.Component {
	_ = Middle()
	return <main><&Card /></main>
}
`
	document := Document{URI: "file:///project/calls.gox", Path: "/project/calls.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	leafItems := server.prepareCallHierarchy(document, referencePosition(t, document, strings.Index(source, "Leaf")))
	if len(leafItems) != 1 {
		t.Fatalf("Leaf hierarchy items = %#v", leafItems)
	}
	incoming := server.incomingCalls(leafItems[0])
	if len(incoming) != 1 || incoming[0].From.Name != "Middle" || len(incoming[0].FromRanges) != 1 {
		t.Fatalf("Leaf incoming calls = %#v", incoming)
	}
	pageItems := server.prepareCallHierarchy(document, referencePosition(t, document, strings.Index(source, "Page")))
	if len(pageItems) != 1 {
		t.Fatalf("Page hierarchy items = %#v", pageItems)
	}
	outgoing := server.outgoingCalls(pageItems[0])
	if len(outgoing) != 2 || outgoing[0].To.Name != "Card" && outgoing[1].To.Name != "Card" || outgoing[0].To.Name != "Middle" && outgoing[1].To.Name != "Middle" {
		t.Fatalf("Page outgoing calls = %#v", outgoing)
	}
}

func TestTypeHierarchyReturnsSupertypesAndSubtypes(t *testing.T) {
	source := `module hierarchy
type Reader interface { Read(value string) string }
type User struct{}
func (User) Read(value string) string { return value }
type Wrong struct{}
func (Wrong) Read(value int) string { return "" }
`
	document := Document{URI: "file:///project/hierarchy.gox", Path: "/project/hierarchy.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	reader := server.prepareTypeHierarchy(document, referencePosition(t, document, strings.Index(source, "Reader")))
	if len(reader) != 1 {
		t.Fatalf("Reader hierarchy = %#v", reader)
	}
	subtypes := server.typeHierarchySubtypes(reader[0])
	if len(subtypes) != 1 || subtypes[0].Name != "User" {
		t.Fatalf("Reader subtypes = %#v", subtypes)
	}
	user := server.prepareTypeHierarchy(document, referencePosition(t, document, strings.Index(source, "User struct")))
	if len(user) != 1 {
		t.Fatalf("User hierarchy = %#v", user)
	}
	supertypes := server.typeHierarchySupertypes(user[0])
	if len(supertypes) != 1 || supertypes[0].Name != "Reader" {
		t.Fatalf("User supertypes = %#v", supertypes)
	}
}

func TestServerAdvertisesAndPreparesHierarchies(t *testing.T) {
	uri := "file:///project/hierarchy.gox"
	source := "module hierarchy\nfunc Leaf() string { return \"leaf\" }\ntype Reader interface { Read() string }\n"
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/prepareCallHierarchy","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":1,"character":6}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"textDocument/prepareTypeHierarchy","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":2,"character":6}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	var initializeResult, callResult, typeResult json.RawMessage
	for _, response := range responses {
		switch string(response.ID) {
		case "1":
			initializeResult = response.Result
		case "2":
			callResult = response.Result
		case "3":
			typeResult = response.Result
		}
	}
	if !bytes.Contains(initializeResult, []byte(`"callHierarchyProvider":true`)) || !bytes.Contains(initializeResult, []byte(`"typeHierarchyProvider":true`)) || string(callResult) == "[]" || string(typeResult) == "[]" {
		t.Fatalf("hierarchy responses = %#v", responses)
	}
}

package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestTypeDefinitionResolvesLocalAndImportedGoxTypes(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	usersSource := "module users\ntype User struct { Name string }\n"
	pageSource := "module page\nimport users \"./users.gox\"\nfunc Display(user users.User) string { return user.Name }\n"
	writeWorkspaceGOX(t, usersPath, usersSource)
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	page, _ := server.symbols.Document(FileURI(pagePath))
	locations := server.typeDefinitionLocations(page, referencePosition(t, page, strings.LastIndex(pageSource, "user.Name")))
	if len(locations) != 1 || locations[0].URI != FileURI(usersPath) || locations[0].Range.Start.Line != 1 {
		t.Fatalf("imported type definition = %#v", locations)
	}

	localSource := "module local\ntype Account struct { ID string }\nfunc ID(value Account) string { return value.ID }\n"
	local := Document{URI: "file:///project/local.gox", Path: "/project/local.gox", Text: localSource}
	if err := server.symbols.UpdateOpen(local); err != nil {
		t.Fatal(err)
	}
	locations = server.typeDefinitionLocations(local, referencePosition(t, local, strings.LastIndex(localSource, "value.ID")))
	if len(locations) != 1 || locations[0].URI != local.URI || locations[0].Range.Start.Line != 1 {
		t.Fatalf("local type definition = %#v", locations)
	}
}

func TestImplementationFindsStructuralInterfaceImplementations(t *testing.T) {
	source := `module contracts
type Reader interface {
	Read(value string) (string, error)
}
type User struct{}
func (User) Read(value string) (string, error) { return value, nil }
type Wrong struct{}
func (Wrong) Read(value int) (string, error) { return "", nil }
`
	document := Document{URI: "file:///project/contracts.gox", Path: "/project/contracts.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	interfaceLocations := server.implementationLocations(document, referencePosition(t, document, strings.Index(source, "Reader")))
	if len(interfaceLocations) != 1 || interfaceLocations[0].Range.Start.Line != 4 {
		t.Fatalf("interface implementations = %#v", interfaceLocations)
	}
	interfaceMethod := strings.Index(source, "Read(value string)")
	methodLocations := server.implementationLocations(document, referencePosition(t, document, interfaceMethod))
	if len(methodLocations) != 1 || methodLocations[0].Range.Start.Line != 5 {
		t.Fatalf("method implementations = %#v", methodLocations)
	}
	concreteLocations := server.implementationLocations(document, referencePosition(t, document, strings.Index(source, "User struct")))
	if len(concreteLocations) != 1 || concreteLocations[0].Range.Start.Line != 1 {
		t.Fatalf("implemented interfaces = %#v", concreteLocations)
	}
}

func TestServerAdvertisesAndReturnsTypeNavigation(t *testing.T) {
	uri := "file:///project/types.gox"
	source := "module types\ntype Name string\nfunc Display(value Name) string { return string(value) }\n"
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/typeDefinition","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":2,"character":18}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	var initializeResult, navigationResult json.RawMessage
	for _, response := range responses {
		switch string(response.ID) {
		case "1":
			initializeResult = response.Result
		case "2":
			navigationResult = response.Result
		}
	}
	if !bytes.Contains(initializeResult, []byte(`"typeDefinitionProvider":true`)) ||
		!bytes.Contains(initializeResult, []byte(`"implementationProvider":true`)) || len(navigationResult) == 0 || string(navigationResult) == "[]" {
		t.Fatalf("type navigation responses = %#v", responses)
	}
}

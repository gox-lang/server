package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentSymbolsBuildHierarchicalOutline(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "account.gox")
	source := `module account
type User struct {
	Name string
}
func (user User) GetName() string { return user.Name }
const DefaultName = "George"
var Enabled = true
func Card() string { return DefaultName }
`
	writeWorkspaceGOX(t, path, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(path))
	if !ok {
		t.Fatal("document was not indexed")
	}

	symbols := server.documentSymbols(document)
	if len(symbols) != 1 || symbols[0].Name != "account" || symbols[0].Kind != 2 {
		t.Fatalf("outline root = %#v", symbols)
	}
	user := findDocumentSymbol(symbols[0].Children, "User")
	if user == nil || user.Kind != 23 || findDocumentSymbol(user.Children, "Name") == nil {
		t.Fatalf("User outline = %#v", user)
	}
	if findDocumentSymbol(user.Children, "GetName") != nil {
		t.Fatalf("receiver method was incorrectly nested inside User: %#v", user)
	}
	for _, name := range []string{"GetName", "DefaultName", "Enabled", "Card"} {
		if findDocumentSymbol(symbols[0].Children, name) == nil {
			t.Fatalf("missing %s in %#v", name, symbols[0].Children)
		}
	}
	defaultPosition, err := document.Position(strings.Index(source, "DefaultName"))
	if err != nil {
		t.Fatal(err)
	}
	if positionInRange(defaultPosition, user.Range) {
		t.Fatalf("DefaultName position %v is inside User range %v", defaultPosition, user.Range)
	}
	card := findDocumentSymbol(symbols[0].Children, "Card")
	cardBody, err := document.Position(strings.Index(source, "return DefaultName"))
	if err != nil {
		t.Fatal(err)
	}
	if card == nil || !positionInRange(cardBody, card.Range) {
		t.Fatalf("Card body position %v is outside Card range %#v", cardBody, card)
	}
}

func TestServerAdvertisesAndReturnsDocumentSymbols(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "card.gox")
	writeWorkspaceGOX(t, path, "module card\nfunc Card() string { return \"George\" }\n")
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/documentSymbol","params":{"textDocument":{"uri":"` + FileURI(path) + `"}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	if len(responses) != 3 || !bytes.Contains(responses[0].Result, []byte(`"documentSymbolProvider":true`)) {
		t.Fatalf("responses = %#v", responses)
	}
	var symbols []DocumentSymbol
	if err := json.Unmarshal(responses[1].Result, &symbols); err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 1 || symbols[0].Name != "card" || findDocumentSymbol(symbols[0].Children, "Card") == nil {
		t.Fatalf("symbols = %#v", symbols)
	}
}

func TestServerReturnsEmptyDocumentSymbolsWhileDocumentIsNotIndexed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "card.gox")
	incomplete := "module card\nfunc Card() { return <&card.Card name={{ }} /> }\n"
	encoded, err := json.Marshal(incomplete)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + FileURI(path) + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/documentSymbol","params":{"textDocument":{"uri":"` + FileURI(path) + `"}}}`),
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
		if response.Error != nil || string(response.Result) != "[]" {
			t.Fatalf("document symbols response = %#v", response)
		}
		return
	}
	t.Fatal("document symbols response not found")
}

func findDocumentSymbol(symbols []DocumentSymbol, name string) *DocumentSymbol {
	for index := range symbols {
		if symbols[index].Name == name {
			return &symbols[index]
		}
	}
	return nil
}

package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceSymbolsSearchIndexedFilesAndDisambiguateModules(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "components", "card.gox")
	secondPath := filepath.Join(root, "pages", "card.gox")
	writeWorkspaceGOX(t, firstPath, "module components\ntype Card struct { Title string }\nfunc NewCard() Card { return Card{} }\n")
	writeWorkspaceGOX(t, secondPath, "module pages\nfunc Card() string { return \"George\" }\n")
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})

	results := server.workspaceSymbols("card")
	if len(results) != 3 {
		t.Fatalf("Card results = %#v", results)
	}
	containers := map[string]bool{}
	for _, result := range results {
		containers[result.ContainerName] = true
		if result.Location.URI != FileURI(firstPath) && result.Location.URI != FileURI(secondPath) {
			t.Fatalf("unexpected location = %#v", result.Location)
		}
	}
	if !containers["components"] || !containers["pages"] {
		t.Fatalf("containers = %#v", containers)
	}
	if fuzzy := server.workspaceSymbols("ncrd"); len(fuzzy) != 1 || fuzzy[0].Name != "NewCard" {
		t.Fatalf("fuzzy results = %#v", fuzzy)
	}
}

func TestWorkspaceSymbolsExcludeLocalComponentsAndTrackOpenChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "page.gox")
	disk := `module page
func Card() string { return "George" }
`
	writeWorkspaceGOX(t, path, disk)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	uri := FileURI(path)
	open := Document{URI: uri, Path: path, LanguageID: "gox", Version: 1, Text: `module page
import "github.com/gox-lang/gox"
func Panel() gox.Component {
	badge := <span>New</span>
	return <main><&badge /></main>
}
`}
	if err := server.symbols.UpdateOpen(open); err != nil {
		t.Fatal(err)
	}
	if results := server.workspaceSymbols("Card"); len(results) != 0 {
		t.Fatalf("stale disk results = %#v", results)
	}
	if results := server.workspaceSymbols("Panel"); len(results) != 1 || results[0].Name != "Panel" {
		t.Fatalf("Panel results = %#v", results)
	}
	if results := server.workspaceSymbols("badge"); len(results) != 0 {
		t.Fatalf("local results = %#v", results)
	}
	server.symbols.CloseOpen(uri)
	if results := server.workspaceSymbols("Card"); len(results) != 1 || results[0].Name != "Card" {
		t.Fatalf("restored disk results = %#v", results)
	}
}

func TestServerAdvertisesAndReturnsWorkspaceSymbols(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "users.gox")
	writeWorkspaceGOX(t, path, "module users\ntype User struct { Name string }\n")
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"workspace/symbol","params":{"query":"use"}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	if len(responses) != 3 || !bytes.Contains(responses[0].Result, []byte(`"workspaceSymbolProvider":true`)) {
		t.Fatalf("responses = %#v", responses)
	}
	var symbols []SymbolInformation
	if err := json.Unmarshal(responses[1].Result, &symbols); err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 2 || symbols[0].Name != "User" && symbols[1].Name != "User" {
		t.Fatalf("symbols = %#v", symbols)
	}
}

package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestIdentifierAtPositionHandlesUTF16AndGOXPrefixes(t *testing.T) {
	document := Document{Text: "😀 <@Menú/> <&Card/>"}
	menuOffset := strings.Index(document.Text, "Menú") + len("Me")
	menuPosition, err := document.Position(menuOffset)
	if err != nil {
		t.Fatal(err)
	}
	name, prefix, err := identifierAtPosition(document, menuPosition)
	if err != nil || name != "Menú" || prefix != '@' {
		t.Fatalf("identifier = %q, %q, %v", name, prefix, err)
	}
	cardOffset := strings.Index(document.Text, "Card") + len("Card")
	cardPosition, err := document.Position(cardOffset)
	if err != nil {
		t.Fatal(err)
	}
	name, prefix, err = identifierAtPosition(document, cardPosition)
	if err != nil || name != "Card" || prefix != '&' {
		t.Fatalf("identifier = %q, %q, %v", name, prefix, err)
	}
}

func TestServerReturnsDefinitionFromUnopenedWorkspaceDocument(t *testing.T) {
	root := t.TempDir()
	componentPath := filepath.Join(root, "components", "card.gox")
	if err := os.MkdirAll(filepath.Dir(componentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(componentPath, []byte("module card\nfunc Card() string { return \"George\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	componentURI := FileURI(componentPath)
	pageURI := FileURI(filepath.Join(root, "pages", "index.gox"))
	pageSource := "module main\nimport card \"../components/card.gox\"\nfunc Render() string { return card.Card() }"
	pageSourceJSON := strconv.Quote(pageSource)
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + pageURI + `","languageId":"gox","version":1,"text":` + pageSourceJSON + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"` + pageURI + `"},"position":{"line":2,"character":37}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	for _, item := range responses {
		if string(item.ID) != "2" {
			continue
		}
		var locations []Location
		if err := json.Unmarshal(item.Result, &locations); err != nil {
			t.Fatal(err)
		}
		if len(locations) != 1 || locations[0].URI != componentURI || locations[0].Range.Start != (Position{Line: 1, Character: 5}) || locations[0].Range.End != (Position{Line: 1, Character: 9}) {
			t.Fatalf("definition locations = %#v", locations)
		}
		return
	}
	t.Fatalf("definition response not found: %s", output.String())
}

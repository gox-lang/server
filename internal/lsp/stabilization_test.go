package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticsAreDeduplicatedAndStable(t *testing.T) {
	first := Diagnostic{
		Range:    Range{Start: Position{Line: 4, Character: 2}, End: Position{Line: 4, Character: 6}},
		Severity: 1, Code: "undefined", Source: "gox", Message: "undefined: Card",
	}
	second := Diagnostic{
		Range:    Range{Start: Position{Line: 1, Character: 0}, End: Position{Line: 1, Character: 4}},
		Severity: 1, Code: "import", Source: "gox", Message: "invalid import",
	}
	result := normalizeDiagnostics([]Diagnostic{first, second, first})
	if len(result) != 2 {
		t.Fatalf("normalized diagnostics = %#v", result)
	}
	if result[0].Code != "import" || result[1].Code != "undefined" {
		t.Fatalf("diagnostics are not source ordered: %#v", result)
	}
}

func TestServerIntegratedEditingRecoveryAndNavigation(t *testing.T) {
	root := t.TempDir()
	cardPath := filepath.Join(root, "card.gox")
	pagePath := filepath.Join(root, "page.gox")
	cardSource := "module card\n\nfunc Card() string { return \"George\" }\n"
	panelSource := "module card\n\nfunc Panel() string { return \"George\" }\n"
	brokenSource := "module card\n\nfunc Card(\n"
	pageSource := "module page\n\nimport card \"card.gox\"\n\nfunc Page() string { return card.Card() }\n"
	if err := os.WriteFile(cardPath, []byte(cardSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o600); err != nil {
		t.Fatal(err)
	}
	cardJSON, _ := json.Marshal(cardSource)
	panelJSON, _ := json.Marshal(panelSource)
	brokenJSON, _ := json.Marshal(brokenSource)
	pageJSON, _ := json.Marshal(pageSource)
	cardURI, pageURI := FileURI(cardPath), FileURI(pagePath)
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + cardURI + `","languageId":"gox","version":1,"text":` + string(cardJSON) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + pageURI + `","languageId":"gox","version":1,"text":` + string(pageJSON) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"workspace/symbol","params":{"query":"Card"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"` + cardURI + `","version":2},"contentChanges":[{"text":` + string(brokenJSON) + `}]}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"` + cardURI + `","version":3},"contentChanges":[{"text":` + string(panelJSON) + `}]}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"` + cardURI + `","version":4},"contentChanges":[{"text":` + string(cardJSON) + `}]}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"textDocument/definition","params":{"textDocument":{"uri":"` + pageURI + `"},"position":{"line":4,"character":35}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":4,"method":"textDocument/documentSymbol","params":{"textDocument":{"uri":"` + cardURI + `"}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":5,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	// Exercise every intermediate diagnostic state; burst coalescing has its own protocol tests.
	server := NewServer()
	server.diagnosticsDebounce = 0
	if err := server.Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	for _, id := range []string{"1", "2", "3", "4", "5"} {
		found := false
		for _, response := range responses {
			if string(response.ID) == id {
				found = true
				if response.Error != nil {
					t.Fatalf("response %s failed: %#v", id, response.Error)
				}
			}
		}
		if !found {
			t.Fatalf("missing response %s in %#v", id, responses)
		}
	}
	var pagePublications [][]Diagnostic
	var cardParseError bool
	for _, message := range decodeFramedMessages(t, output.String()) {
		if message.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var params PublishDiagnosticsParams
		if json.Unmarshal(message.Params, &params) != nil {
			continue
		}
		if params.URI == pageURI {
			pagePublications = append(pagePublications, params.Diagnostics)
		}
		if params.URI == cardURI && params.Version == 2 && len(params.Diagnostics) > 0 {
			cardParseError = true
		}
	}
	if !cardParseError {
		t.Fatal("incomplete source did not publish a parse diagnostic")
	}
	if len(pagePublications) < 4 || len(pagePublications[len(pagePublications)-2]) == 0 || len(pagePublications[len(pagePublications)-1]) != 0 {
		t.Fatalf("dependent diagnostic recovery = %#v", pagePublications)
	}
}

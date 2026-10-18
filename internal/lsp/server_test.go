package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestServerLifecycleOverStdioFraming(t *testing.T) {
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"initialized","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	if len(responses) != 2 {
		t.Fatalf("responses = %d, want 2: %s", len(responses), output.String())
	}
	if string(responses[0].ID) != "1" || !bytes.Contains(responses[0].Result, []byte(`"positionEncoding":"utf-16"`)) || !bytes.Contains(responses[0].Result, []byte(`"textDocumentSync":{"change":1,"openClose":true}`)) || !bytes.Contains(responses[0].Result, []byte(`"semanticTokensProvider"`)) || !bytes.Contains(responses[0].Result, []byte(`"definitionProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"typeDefinitionProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"implementationProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"callHierarchyProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"typeHierarchyProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"referencesProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"documentHighlightProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"documentSymbolProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"foldingRangeProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"selectionRangeProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"documentFormattingProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"documentRangeFormattingProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"workspaceSymbolProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"renameProvider":{"prepareProvider":true}`)) || !bytes.Contains(responses[0].Result, []byte(`"linkedEditingRangeProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"inlayHintProvider":true`)) || !bytes.Contains(responses[0].Result, []byte(`"codeActionProvider"`)) || !bytes.Contains(responses[0].Result, []byte(`"completionProvider":{"triggerCharacters":[".",":","\u0026","@"," ","=","\"","{"]}`)) || !bytes.Contains(responses[0].Result, []byte(`"signatureHelpProvider"`)) || !bytes.Contains(responses[0].Result, []byte(`"documentOnTypeFormattingProvider":{"firstTriggerCharacter":"\u003e"}`)) || !bytes.Contains(responses[0].Result, []byte(`"fileOperations"`)) {
		t.Fatalf("initialize response = %#v", responses[0])
	}
	if string(responses[1].ID) != "2" || string(responses[1].Result) != "null" {
		t.Fatalf("shutdown response = %#v", responses[1])
	}
}

func TestWorkspaceFileOperationsIncludeGoDependencies(t *testing.T) {
	registration := workspaceFileOperationRegistration()
	filters, ok := registration["filters"].([]map[string]any)
	if !ok {
		t.Fatalf("workspace file operation filters = %#v", registration)
	}
	got := make(map[string]bool, len(filters))
	for _, filter := range filters {
		pattern, ok := filter["pattern"].(map[string]any)
		if !ok {
			t.Fatalf("workspace file operation filter = %#v", filter)
		}
		glob, _ := pattern["glob"].(string)
		got[glob] = true
	}
	for _, want := range []string{"**/*.gox", "**/*.go", "**/go.mod", "**/go.sum", "**/go.work"} {
		if !got[want] {
			t.Fatalf("workspace file operation filters = %#v, missing %q", got, want)
		}
	}
}

func TestServerTracksOpenChangedAndClosedDocuments(t *testing.T) {
	uri := "file:///workspace/pages/index.gox"
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":"George"}}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"` + uri + `","version":2},"contentChanges":[{"text":"Hello, George"}]}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	server := NewServer()
	if err := server.Serve(strings.NewReader(input), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	document, ok := server.Document(uri)
	if !ok || document.Version != 2 || document.Text != "Hello, George" {
		t.Fatalf("document = %#v, %v", document, ok)
	}

	closeInput := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didClose","params":{"textDocument":{"uri":"` + uri + `"}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	if err := server.Serve(strings.NewReader(closeInput), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if server.DocumentCount() != 0 {
		t.Fatalf("document count = %d", server.DocumentCount())
	}
}

func TestServerSynchronizesWorkspaceSymbolsWithDocumentLifecycle(t *testing.T) {
	uri := "file:///workspace/components/card.gox"
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":"module card\nfunc Card() string { return \"George\" }"}}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"` + uri + `","version":2},"contentChanges":[{"text":"module card\nfunc Panel() string { return \"George\" }"}]}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	server := NewServer()
	if err := server.Serve(strings.NewReader(input), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if definitions := server.SymbolDefinitions("Card"); len(definitions) != 0 {
		t.Fatalf("stale Card definitions = %#v", definitions)
	}
	if definitions := server.SymbolDefinitions("Panel"); len(definitions) != 1 || definitions[0].URI != uri {
		t.Fatalf("Panel definitions = %#v", definitions)
	}

	closeInput := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didClose","params":{"textDocument":{"uri":"` + uri + `"}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	if err := server.Serve(strings.NewReader(closeInput), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if definitions := server.SymbolDefinitions("Panel"); len(definitions) != 0 {
		t.Fatalf("Panel definitions after close = %#v", definitions)
	}
}

func TestServerPublishesDiagnosticsFromUnsavedDocumentVersions(t *testing.T) {
	uri := "file:///workspace/pages/index.gox"
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":"module main\nfunc Broken() {\nname :=\n}"}}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"` + uri + `","version":2},"contentChanges":[{"text":"module main\n"}]}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	messages := decodeFramedMessages(t, output.String())
	var published []PublishDiagnosticsParams
	for _, item := range messages {
		if item.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var params PublishDiagnosticsParams
		if err := json.Unmarshal(item.Params, &params); err != nil {
			t.Fatal(err)
		}
		published = append(published, params)
	}
	if len(published) != 2 {
		t.Fatalf("published diagnostics = %#v", published)
	}
	if published[0].Version != 1 || len(published[0].Diagnostics) != 1 {
		t.Fatalf("version 1 diagnostics = %#v", published[0])
	}
	if published[1].Version != 2 || len(published[1].Diagnostics) != 0 {
		t.Fatalf("version 2 diagnostics = %#v", published[1])
	}
}

func TestServerReturnsFullSemanticTokensForOpenDocument(t *testing.T) {
	uri := "file:///workspace/pages/index.gox"
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":"module main\nimport \"github.com/gox-lang/gox\"\nfunc Card() gox.Component { return <span>George</span> }\nfunc Render() gox.Component { return <main><&Card/></main> }"}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/semanticTokens/full","params":{"textDocument":{"uri":"` + uri + `"}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	var semanticResponse *response
	for index := range responses {
		if string(responses[index].ID) == "2" {
			semanticResponse = &responses[index]
			break
		}
	}
	if semanticResponse == nil || !bytes.Contains(semanticResponse.Result, []byte(`"data":[`)) {
		t.Fatalf("semantic response = %#v", semanticResponse)
	}
}

func TestServerRejectsExitBeforeShutdown(t *testing.T) {
	input := framedMessage(`{"jsonrpc":"2.0","method":"exit"}`)
	if err := NewServer().Serve(strings.NewReader(input), &bytes.Buffer{}); err != ErrExitWithoutShutdown {
		t.Fatalf("error = %v, want %v", err, ErrExitWithoutShutdown)
	}
}

func TestServerReturnsMethodNotFound(t *testing.T) {
	input := framedMessage(`{"jsonrpc":"2.0","id":"x","method":"gox/unknown"}`)
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	if len(responses) != 1 || responses[0].Error == nil || responses[0].Error.Code != -32601 {
		t.Fatalf("responses = %#v", responses)
	}
}

func framedMessage(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func decodeFramedResponses(t *testing.T, stream string) []response {
	t.Helper()
	conn := newConnection(strings.NewReader(stream), &bytes.Buffer{})
	var responses []response
	for {
		body, err := conn.read()
		if err != nil {
			break
		}
		var item response
		if err := json.Unmarshal(body, &item); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, item)
	}
	return responses
}

func decodeFramedMessages(t *testing.T, stream string) []message {
	t.Helper()
	conn := newConnection(strings.NewReader(stream), &bytes.Buffer{})
	var messages []message
	for {
		body, err := conn.read()
		if err != nil {
			break
		}
		var item message
		if err := json.Unmarshal(body, &item); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, item)
	}
	return messages
}

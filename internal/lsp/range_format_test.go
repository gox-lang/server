package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRangeFormattingFormatsSelectedDeclarationWithIncompleteDocument(t *testing.T) {
	source := "module page\n\nfunc Ready( value int )int{return value+1}\n\nfunc Broken(\n"
	document := Document{URI: "file:///page.gox", Path: "/page.gox", Text: source}
	edits := documentRangeFormattingEdits(document, Range{
		Start: Position{Line: 2},
		End:   Position{Line: 3},
	})
	if len(edits) != 1 || edits[0].NewText != "func Ready(value int) int { return value + 1 }\n" {
		t.Fatalf("range formatting edits = %#v", edits)
	}
	formatted := applyFormattingEdit(t, document, edits[0])
	if !strings.HasSuffix(formatted, "\nfunc Broken(\n") {
		t.Fatalf("incomplete declaration changed:\n%s", formatted)
	}
}

func TestRangeFormattingFormatsSelectedStatementsAndTemplate(t *testing.T) {
	source := `module page
func Card(name string) {
	value:=1
	_ = value
	content:=<article><h1>{{name}}</h1></article>
	_ = content
}
`
	document := Document{URI: "file:///page.gox", Path: "/page.gox", Text: source}
	statementEdits := documentRangeFormattingEdits(document, Range{
		Start: Position{Line: 2},
		End:   Position{Line: 4},
	})
	if len(statementEdits) != 1 || statementEdits[0].NewText != "\tvalue := 1\n\t_ = value\n" {
		t.Fatalf("statement edits = %#v", statementEdits)
	}
	templateEdits := documentRangeFormattingEdits(document, Range{
		Start: Position{Line: 4},
		End:   Position{Line: 5},
	})
	if len(templateEdits) != 1 {
		t.Fatalf("template edits = %#v", templateEdits)
	}
	want := "\tcontent := <article>\n\t\t<h1>{{ name }}</h1>\n\t</article>\n"
	if templateEdits[0].NewText != want {
		t.Fatalf("template edit = %q, want %q", templateEdits[0].NewText, want)
	}
}

func TestDocumentFormattingFormatsCompletePrefixAndPreservesIncompleteSuffix(t *testing.T) {
	source := "module page\nfunc Ready( value int )int{return value+1}\nfunc Broken(\n"
	document := Document{URI: "file:///page.gox", Path: "/page.gox", Text: source}
	edits := documentFormattingEdits(document)
	if len(edits) != 1 {
		t.Fatalf("incomplete document formatting edits = %#v", edits)
	}
	formatted := applyFormattingEdit(t, document, edits[0])
	if !strings.Contains(formatted, "func Ready(value int) int { return value + 1 }") ||
		!strings.HasSuffix(formatted, "func Broken(\n") {
		t.Fatalf("formatted incomplete document:\n%s", formatted)
	}
}

func TestServerAdvertisesAndReturnsRangeFormatting(t *testing.T) {
	source := "module page\nfunc Ready( value int )int{return value+1}\nfunc Broken(\n"
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	uri := "file:///page.gox"
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/rangeFormatting","params":{"textDocument":{"uri":"` + uri + `"},"range":{"start":{"line":1,"character":0},"end":{"line":2,"character":0}},"options":{"tabSize":4,"insertSpaces":false}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	var initializeResult, formattingResult json.RawMessage
	for _, response := range responses {
		switch string(response.ID) {
		case "1":
			initializeResult = response.Result
		case "2":
			formattingResult = response.Result
		}
	}
	if !bytes.Contains(initializeResult, []byte(`"documentRangeFormattingProvider":true`)) ||
		!bytes.Contains(formattingResult, []byte(`func Ready(value int) int`)) {
		t.Fatalf("responses = %#v", responses)
	}
}

func applyFormattingEdit(t *testing.T, document Document, edit CompletionTextEdit) string {
	t.Helper()
	start, startErr := document.Offset(edit.Range.Start)
	end, endErr := document.Offset(edit.Range.End)
	if startErr != nil || endErr != nil {
		t.Fatalf("edit offsets: %v, %v", startErr, endErr)
	}
	return document.Text[:start] + edit.NewText + document.Text[end:]
}

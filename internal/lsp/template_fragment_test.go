package lsp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateFragmentClosingEditsAssignmentAndNestedMarkup(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "page.gox")
	cases := []string{
		"func Card() { menu := <>/*cursor*/ }",
		"func Card() { var menu gox.Component = <>/*cursor*/ }",
		"func Card() { return <main><>/*cursor*/</main> }",
	}
	for _, source := range cases {
		offset := strings.Index(source, "/*cursor*/")
		text := strings.Replace(source, "/*cursor*/", "", 1)
		document := Document{URI: FileURI(path), Path: path, Text: text}
		position, err := document.Position(offset)
		if err != nil {
			t.Fatal(err)
		}
		edits := templateFragmentClosingEdits(document, position, ">")
		if len(edits) != 1 || edits[0].NewText != "</>" || edits[0].Range.Start != position || edits[0].Range.End != position {
			t.Fatalf("fragment edits for %q = %#v", source, edits)
		}
	}
}

func TestTemplateFragmentClosingEditsRejectsGoAndExistingClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	for _, source := range []string{"func Compare() { value := left <>/*cursor*/ right }", "func Card() { menu := <>/*cursor*/</> }"} {
		offset := strings.Index(source, "/*cursor*/")
		text := strings.Replace(source, "/*cursor*/", "", 1)
		document := Document{URI: FileURI(path), Path: path, Text: text}
		position, err := document.Position(offset)
		if err != nil {
			t.Fatal(err)
		}
		if edits := templateFragmentClosingEdits(document, position, ">"); len(edits) != 0 {
			t.Fatalf("unexpected fragment edits for %q: %#v", source, edits)
		}
	}
}

func TestServerProvidesTemplateFragmentOnTypeFormatting(t *testing.T) {
	uri := FileURI(filepath.Join(t.TempDir(), "page.gox"))
	source := "module page\nfunc Card() { menu := <> }\n"
	document := Document{URI: uri, Text: source}
	offset := strings.Index(source, "<>") + 2
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	positionJSON, err := json.Marshal(position)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + jsonString(t, source) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/onTypeFormatting","params":{"textDocument":{"uri":"` + uri + `"},"position":` + string(positionJSON) + `,"ch":">","options":{"tabSize":4,"insertSpaces":false}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit","params":{}}`),
	}, "")
	var output strings.Builder
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	for _, item := range responses {
		if string(item.ID) == "2" && strings.Contains(string(item.Result), `"newText":"\u003c/\u003e"`) {
			return
		}
	}
	t.Fatalf("on-type formatting response = %#v", responses)
}

func jsonString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDocumentHighlightsClassifyLocalReadsAndWrites(t *testing.T) {
	source := `module page
func Card() gox.Component {
	count := 0
	count = count + 1
	count++
	return <span>{{ count }}</span>
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	position := referencePosition(t, document, strings.Index(source, "count :="))
	highlights := server.documentHighlights(document, position)
	want := []struct {
		offset int
		kind   int
	}{
		{strings.Index(source, "count :="), documentHighlightWrite},
		{strings.Index(source, "count ="), documentHighlightWrite},
		{strings.Index(source, "count + 1"), documentHighlightRead},
		{strings.Index(source, "count++"), documentHighlightWrite},
		{strings.Index(source, "count }}"), documentHighlightRead},
	}
	assertDocumentHighlights(t, document, highlights, want)
}

func TestDocumentHighlightsKeepTypedMembersSeparate(t *testing.T) {
	source := `module page
type User struct { Name string }
type Profile struct { Name string }
func Build(user User, profile Profile) {
	_ = user.Name
	user.Name = "George"
	_ = profile.Name
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	declaration := strings.Index(source, "Name string")
	highlights := server.documentHighlights(document, referencePosition(t, document, declaration))
	want := []struct {
		offset int
		kind   int
	}{
		{declaration, documentHighlightWrite},
		{strings.Index(source, "user.Name") + len("user."), documentHighlightRead},
		{strings.LastIndex(source, "user.Name") + len("user."), documentHighlightWrite},
	}
	assertDocumentHighlights(t, document, highlights, want)
}

func TestDocumentHighlightsOnlyReturnTheActiveDocument(t *testing.T) {
	server := NewServer()
	firstSource := "module first\nfunc Join(value string) string { return value }\nfunc Build() { _ = Join(\"George\") }\n"
	secondSource := "module second\nfunc Join(value string) string { return value }\nfunc Build() { _ = Join(\"Milo\") }\n"
	first := Document{URI: "file:///project/first.gox", Path: "/project/first.gox", Text: firstSource}
	second := Document{URI: "file:///project/second.gox", Path: "/project/second.gox", Text: secondSource}
	if err := server.symbols.UpdateOpen(first); err != nil {
		t.Fatal(err)
	}
	if err := server.symbols.UpdateOpen(second); err != nil {
		t.Fatal(err)
	}
	highlights := server.documentHighlights(first, referencePosition(t, first, strings.LastIndex(firstSource, "Join")))
	if len(highlights) != 2 {
		t.Fatalf("first document highlights = %#v", highlights)
	}
	for _, highlight := range highlights {
		if highlight.Range.Start.Line != 1 && highlight.Range.Start.Line != 2 {
			t.Fatalf("unexpected highlight = %#v", highlight)
		}
	}
}

func TestServerReturnsDocumentHighlightsOverLSP(t *testing.T) {
	uri := "file:///project/page.gox"
	source := "module page\nfunc Build() { name := \"George\"; _ = name }\n"
	document := Document{URI: uri, Path: "/project/page.gox", Text: source}
	position := referencePosition(t, document, strings.LastIndex(source, "name"))
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/documentHighlight","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":` + referenceInt(position.Line) + `,"character":` + referenceInt(position.Character) + `}}}`),
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
		var highlights []DocumentHighlight
		if err := json.Unmarshal(response.Result, &highlights); err != nil {
			t.Fatal(err)
		}
		if len(highlights) != 2 || highlights[0].Kind != documentHighlightWrite || highlights[1].Kind != documentHighlightRead {
			t.Fatalf("document highlights = %#v", highlights)
		}
		return
	}
	t.Fatalf("document highlight response not found: %s", output.String())
}

func assertDocumentHighlights(t *testing.T, document Document, highlights []DocumentHighlight, want []struct {
	offset int
	kind   int
}) {
	t.Helper()
	if len(highlights) != len(want) {
		t.Fatalf("highlights = %#v, want %#v", highlights, want)
	}
	for index, expected := range want {
		position := referencePosition(t, document, expected.offset)
		if highlights[index].Range.Start != position || highlights[index].Kind != expected.kind {
			t.Fatalf("highlight %d = %#v, want offset %d kind %d", index, highlights[index], expected.offset, expected.kind)
		}
	}
}

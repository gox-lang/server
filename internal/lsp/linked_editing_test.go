package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLinkedEditingPairsHTMLCustomDirectiveComponentAndClientTags(t *testing.T) {
	source := `module page
func Build() gox.Component {
	return <main><section></section><my-widget></my-widget><:if condition={{ true }}></:if><&card.Card></&card.Card><@Menu></@Menu></main>
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	for _, name := range []string{"main", "section", "my-widget", "if", "card.Card", "Menu"} {
		opening := strings.Index(source, name)
		closing := strings.LastIndex(source, name)
		result := linkedEditingRanges(document, referencePosition(t, document, opening))
		assertLinkedEditingOffsets(t, document, result, opening, closing)
	}
}

func TestLinkedEditingRespectsNestedEqualTags(t *testing.T) {
	source := "module page\nfunc Build() gox.Component { return <div><div>inner</div></div> }\n"
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	outerOpen := strings.Index(source, "div")
	innerOpen := strings.Index(source[outerOpen+3:], "div") + outerOpen + 3
	innerClose := strings.Index(source[innerOpen+3:], "div") + innerOpen + 3
	outerClose := strings.LastIndex(source, "div")
	assertLinkedEditingOffsets(t, document, linkedEditingRanges(document, referencePosition(t, document, outerOpen)), outerOpen, outerClose)
	assertLinkedEditingOffsets(t, document, linkedEditingRanges(document, referencePosition(t, document, innerOpen)), innerOpen, innerClose)
}

func TestLinkedEditingIgnoresSelfClosingTagsAndExpressionOperators(t *testing.T) {
	source := "module page\nfunc Build(a, b int) gox.Component { return <main><&Card /><span>{{ a < b }}</span></main> }\n"
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	card := strings.Index(source, "Card")
	if result := linkedEditingRanges(document, referencePosition(t, document, card)); result != nil {
		t.Fatalf("self-closing linked ranges = %#v", result)
	}
	spanOpen := strings.Index(source, "span")
	spanClose := strings.LastIndex(source, "span")
	assertLinkedEditingOffsets(t, document, linkedEditingRanges(document, referencePosition(t, document, spanClose)), spanOpen, spanClose)
}

func TestServerReturnsLinkedEditingRanges(t *testing.T) {
	uri := "file:///project/page.gox"
	source := "module page\nfunc Build() gox.Component { return <article>Hi</article> }\n"
	document := Document{URI: uri, Path: "/project/page.gox", Text: source}
	position := referencePosition(t, document, strings.Index(source, "article"))
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/linkedEditingRange","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":` + referenceInt(position.Line) + `,"character":` + referenceInt(position.Character) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	var result json.RawMessage
	for _, response := range decodeFramedResponses(t, output.String()) {
		if string(response.ID) == "2" {
			result = response.Result
		}
	}
	if !bytes.Contains(result, []byte(`"ranges"`)) || !bytes.Contains(result, []byte(`"wordPattern"`)) {
		t.Fatalf("linked editing result = %s", result)
	}
}

func assertLinkedEditingOffsets(t *testing.T, document Document, result *LinkedEditingRanges, open, close int) {
	t.Helper()
	if result == nil || len(result.Ranges) != 2 {
		t.Fatalf("linked editing result = %#v", result)
	}
	first, firstErr := document.Offset(result.Ranges[0].Start)
	second, secondErr := document.Offset(result.Ranges[1].Start)
	if firstErr != nil || secondErr != nil || first != open || second != close {
		t.Fatalf("linked editing offsets = (%d, %d), want (%d, %d)", first, second, open, close)
	}
}

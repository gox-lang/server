package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFoldingRangesCoverGoImportsTemplatesDirectivesAndComments(t *testing.T) {
	source := `module page
import (
	fmt "fmt"
	gox "github.com/gox-lang/gox"
)
/*
component documentation
*/
func Card(name string) gox.Component {
	return <article>
		<header>
			<h1>{{ name }}</h1>
		</header>
		<:if condition={{ name != "" }}>
			<p>{{ fmt.Sprint(name) }}</p>
		</:if>
	</article>
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	ranges := foldingRanges(document)
	for _, expected := range []struct {
		start int
		kind  string
	}{
		{start: 1, kind: "imports"},
		{start: 5, kind: "comment"},
		{start: 8, kind: "region"},
		{start: 9, kind: "region"},
		{start: 13, kind: "region"},
	} {
		if !hasFoldingRange(ranges, expected.start, expected.kind) {
			t.Fatalf("missing folding range at line %d kind %q in %#v", expected.start, expected.kind, ranges)
		}
	}
}

func TestSelectionRangesExpandIdentifierThroughTemplateAndFunction(t *testing.T) {
	source := `module page
func Card(name string) gox.Component {
	return <article>
		<h1>{{ name }}</h1>
	</article>
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	position := referencePosition(t, document, strings.LastIndex(source, "name"))
	ranges := selectionRanges(document, []Position{position})
	if len(ranges) != 1 || ranges[0].Parent == nil || ranges[0].Parent.Parent == nil {
		t.Fatalf("selection ranges = %#v", ranges)
	}
	foundTemplate := false
	for current := &ranges[0]; current != nil; current = current.Parent {
		if current.Range.Start.Line == 2 {
			foundTemplate = true
		}
	}
	if ranges[0].Range.Start.Line != 3 || !foundTemplate {
		t.Fatalf("selection hierarchy = %#v", ranges[0])
	}
}

func TestStructuralRangesRemainAvailableForIncompleteDocument(t *testing.T) {
	source := "module page\nfunc Broken() {\n\tcontent := <article>\n\t\t<section>\n"
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	ranges := foldingRanges(document)
	if !hasFoldingRange(ranges, 1, "region") || !hasFoldingRange(ranges, 2, "region") {
		t.Fatalf("incomplete folding ranges = %#v", ranges)
	}
}

func TestServerAdvertisesAndReturnsStructuralRanges(t *testing.T) {
	uri := "file:///project/page.gox"
	source := "module page\nfunc Card() {\n\tif true {\n\t}\n}\n"
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/foldingRange","params":{"textDocument":{"uri":"` + uri + `"}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"textDocument/selectionRange","params":{"textDocument":{"uri":"` + uri + `"},"positions":[{"line":2,"character":4}]}}`),
		framedMessage(`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	var initializeResult, foldingResult, selectionResult json.RawMessage
	for _, response := range responses {
		switch string(response.ID) {
		case "1":
			initializeResult = response.Result
		case "2":
			foldingResult = response.Result
		case "3":
			selectionResult = response.Result
		}
	}
	if !bytes.Contains(initializeResult, []byte(`"foldingRangeProvider":true`)) ||
		!bytes.Contains(initializeResult, []byte(`"selectionRangeProvider":true`)) || len(foldingResult) == 0 || string(foldingResult) == "[]" ||
		!bytes.Contains(selectionResult, []byte(`"parent"`)) {
		t.Fatalf("structural range responses = %#v", responses)
	}
}

func hasFoldingRange(ranges []FoldingRange, start int, kind string) bool {
	for _, value := range ranges {
		if value.StartLine == start && value.Kind == kind {
			return true
		}
	}
	return false
}

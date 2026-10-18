package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentFormattingFormatsGoAndPreservesTemplate(t *testing.T) {
	source := "module page\nfunc Build( name string )string{return <h1>{{  name }}</h1>}\n"
	document := Document{URI: "file:///page.gox", Path: "/page.gox", Text: source}
	edits := documentFormattingEdits(document)
	if len(edits) != 1 || !strings.Contains(edits[0].NewText, "func Build(name string) string {") ||
		!strings.Contains(edits[0].NewText, "<h1>{{ name }}</h1>") {
		t.Fatalf("formatting edits = %#v", edits)
	}
}

func TestDocumentFormattingFormatsValidTemplatePrefixBeforeIncompleteDeclaration(t *testing.T) {
	source := `module rangeformat

import (
fmt "fmt"
gox "github.com/gox-lang/gox"
strings "strings"
)

type User struct {
Name string
Age int
Active bool
}

func DisplayName(user User)string{
name:=strings.TrimSpace(user.Name)
return fmt.Sprintf("%s (%d)",name,user.Age)
}

func Card(user User,children gox.Component)gox.Component{
return <article class="card"><h2>{{user.Name}}</h2><&children /></article>
}

func Broken(
`
	document := Document{URI: "file:///rangeformat.gox", Path: "/rangeformat.gox", Text: source}
	edits := documentFormattingEdits(document)
	if len(edits) != 1 {
		t.Fatalf("formatting edits = %#v", edits)
	}
	formatted := applyFormattingEdit(t, document, edits[0])
	for _, want := range []string{
		"\tfmt \"fmt\"",
		"type User struct {\n\tName   string",
		"func DisplayName(user User) string {",
		"return <article class=\"card\">\n\t\t<h2>{{ user.Name }}</h2>",
		"func Broken(\n",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted document does not contain %q:\n%s", want, formatted)
		}
	}
}

func TestServerAdvertisesAndReturnsDocumentFormatting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "page.gox")
	source := "module page\nfunc Build( value int )int{return value+1}\n"
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + FileURI(path) + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/formatting","params":{"textDocument":{"uri":"` + FileURI(path) + `"},"options":{"tabSize":4,"insertSpaces":false}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	var initializeResult json.RawMessage
	var formattingResult json.RawMessage
	for _, response := range responses {
		switch string(response.ID) {
		case "1":
			initializeResult = response.Result
		case "2":
			formattingResult = response.Result
		}
	}
	if !bytes.Contains(initializeResult, []byte(`"documentFormattingProvider":true`)) ||
		!bytes.Contains(formattingResult, []byte(`func Build(value int) int`)) {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestDocumentFormattingDoesNotAccumulateStyleOrScriptIndentation(t *testing.T) {
	source := `module page

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <main>
		<style>
					.menu {
						border: 0 0 0 1px
					}
				</style>
		<script>
					if (a < b) {
						console.log(a)
					}
				</script>
	</main>
}
`
	document := Document{URI: "file:///page.gox", Path: "/page.gox", Text: source}
	edits := documentFormattingEdits(document)
	if len(edits) != 1 {
		t.Fatalf("first formatting edits = %#v", edits)
	}
	formatted := applyFormattingEdit(t, document, edits[0])
	for _, want := range []string{
		"\t\t<style>\n\t\t\t.menu {\n\t\t\t\tborder: 0 0 0 1px\n\t\t\t}\n\t\t</style>",
		"\t\t<script>\n\t\t\tif (a < b) {\n\t\t\t\tconsole.log(a)\n\t\t\t}\n\t\t</script>",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted document does not contain stable embedded block %q:\n%s", want, formatted)
		}
	}

	formattedDocument := Document{URI: document.URI, Path: document.Path, Text: formatted}
	if again := documentFormattingEdits(formattedDocument); len(again) != 0 {
		t.Fatalf("second formatting must be a no-op, edits = %#v", again)
	}
}

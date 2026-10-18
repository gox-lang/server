package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerReturnsLexicalCompletionOverLSP(t *testing.T) {
	uri := "file:///workspace/page.gox"
	source := "module page\nfunc Build() {\n\tuser := 10\n\tus\n}\n"
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":3,"character":3}}}`),
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
		var result CompletionList
		if err := json.Unmarshal(response.Result, &result); err != nil {
			t.Fatal(err)
		}
		assertCompletionLabels(t, result.Items, "user")
		return
	}
	t.Fatalf("completion response not found: %s", output.String())
}

func TestLexicalCompletionProvidesVisibleGoSymbolsAndImports(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeWorkspaceGOX(t, filepath.Join(root, "accounts.gox"), "module accounts\ntype Account struct { Name string }\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import (
	"accounts.gox"
	"fmt"
)
type User struct { Name string }
const Limit = 10
func Build(parameter string) {
	user := User{Name: parameter}
	us/*local*/
	par/*parameter*/
	Li/*module*/
	acc/*gox-import*/
	fm/*go-import*/
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	assertCompletionLabels(t, lexicalItemsAtMarker(t, server, document, "/*local*/"), "user")
	requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*parameter*/"), "parameter")
	limit := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*module*/"), "Limit")
	if len(limit.AdditionalTextEdits) != 0 {
		t.Fatalf("local module symbol Limit received auto-import edits: %#v", limit)
	}
	assertCompletionLabels(t, lexicalItemsAtMarker(t, server, document, "/*gox-import*/"), "accounts")
	assertCompletionLabels(t, lexicalItemsAtMarker(t, server, document, "/*go-import*/"), "fmt")
}

func TestLexicalCompletionRespectsTemplateLoopScope(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
type User struct { Name string }
func Card() string {
	users := []User{{Name: "George"}}
	return <section>
		<:for index user range={{ users }}>
			<span>{{ use/*inside*/ }}</span>
		</:for>
		<span>{{ use/*outside*/ }}</span>
	</section>
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	assertCompletionLabels(t, lexicalItemsAtMarker(t, server, document, "/*inside*/"), "user", "users")
	outside := lexicalItemsAtMarker(t, server, document, "/*outside*/")
	requireCompletionItem(t, outside, "users")
	for _, item := range outside {
		if item.Label == "user" && len(item.AdditionalTextEdits) == 0 {
			t.Fatalf("loop variable leaked outside its scope: %#v", item)
		}
	}
}

func TestLexicalCompletionRespectsShadowingAndDeclarationOrder(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Build() {
	value := 10
	{
		value := "George"
		val/*inner*/
		fu/*before*/
		future := true
		_ = future
	}
	val/*outer*/
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	inner := lexicalItemsAtMarker(t, server, document, "/*inner*/")
	assertCompletionLabels(t, inner, "value")
	if inner[0].Detail != "var value string" {
		t.Fatalf("inner value completion = %#v", inner[0])
	}
	assertCompletionLabels(t, lexicalItemsAtMarker(t, server, document, "/*before*/"))
	outer := lexicalItemsAtMarker(t, server, document, "/*outer*/")
	assertCompletionLabels(t, outer, "value")
	if outer[0].Detail != "var value int" {
		t.Fatalf("outer value completion = %#v", outer[0])
	}
}

func TestLexicalCompletionSupportsEmptyTemplateExpression(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Card(name string) string {
	user := name
	return <span>{{ /*cursor*/ }}</span>
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	for _, label := range []string{"name", "user", "Card", "for", "go", "if", "switch", "append", "make", "recover"} {
		requireCompletionItem(t, items, label)
	}
}

func TestLexicalCompletionDoesNotRunInTemplateTextStringsOrComments(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Card() string {
	user := "George"
	_ = "us/*string*/"
	// us/*comment*/
	return <span>us/*html*/</span>
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	assertCompletionLabels(t, lexicalItemsAtMarker(t, server, document, "/*string*/"))
	assertCompletionLabels(t, lexicalItemsAtMarker(t, server, document, "/*comment*/"))
	assertCompletionLabels(t, lexicalItemsAtMarker(t, server, document, "/*html*/"))
}

func lexicalItemsAtMarker(t *testing.T, server *Server, document Document, marker string) []CompletionItem {
	t.Helper()
	offset := strings.Index(document.Text, marker)
	if offset < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	return server.completions(document, position).Items
}

func TestLexicalCompletionIncludesGoBuiltins(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Build() {
	rec/*recover*/
	app/*append*/
	ma/*make*/
	pri/*println*/
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	for marker, label := range map[string]string{
		"/*recover*/": "recover",
		"/*append*/":  "append",
		"/*make*/":    "make",
		"/*println*/": "println",
	} {
		item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, marker), label)
		if item.Kind != 3 {
			t.Fatalf("%s completion kind = %d, want function (3): %#v", label, item.Kind, item)
		}
		if item.Detail == "" || len(item.CandidateParameters) == 0 && label != "recover" {
			t.Fatalf("%s completion lacks builtin signature metadata: %#v", label, item)
		}
	}
}

func TestLexicalCompletionLocalSymbolShadowsGoBuiltin(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Build() {
	len := func() string { return "local" }
	le/*cursor*/
	_ = len
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")

	count := 0
	var found CompletionItem
	for _, item := range items {
		if item.Label == "len" {
			count++
			found = item
		}
	}
	if count != 1 {
		t.Fatalf("len completion count = %d, want 1: %#v", count, items)
	}
	if found.Detail == "func len(v Type) int" {
		t.Fatalf("builtin len must not replace local len: %#v", found)
	}
}

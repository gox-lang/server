package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestTemplateCompletionExpandsHTMLWithoutOpeningBracket(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Card() string {
	return <main>
		di/*div*/
		a/*anchor*/
		in/*input*/
	</main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	div := lexicalItemsAtMarker(t, server, document, "/*div*/")
	assertCompletionContainsSnippet(t, div, "div", "<div>${1}</div>")
	anchor := lexicalItemsAtMarker(t, server, document, "/*anchor*/")
	assertCompletionContainsSnippet(t, anchor, "a", `<a href="${1}">${2}</a>`)
	input := lexicalItemsAtMarker(t, server, document, "/*input*/")
	assertCompletionContainsSnippet(t, input, "input", `<input type="${1:text}" name="${2}">`)
}

func TestTemplateCompletionRecognizesExpectedComponentReturn(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import gox "github.com/gox-lang/gox"
func Card() gox.Component {
	return sec/*cursor*/
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	assertCompletionContainsSnippet(t, items, "section", "<section>${1}</section>")
}

func TestTemplateCompletionRecognizesExpectedPageReturn(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "index.gox")
	pageSource := `module index
import gox "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page {
	return ma/*cursor*/
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	assertCompletionContainsSnippet(t, items, "main", "<main>${1}</main>")
}

func TestTemplateCompletionRecognizesAssignmentAndCallArgumentValues(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import gox "github.com/gox-lang/gox"
func Card() gox.Component {
	var menu gox.Component = di/*explicit*/
	short := sec/*short*/
	Render(spa/*argument*/)
	return <main><&menu/><&short/></main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	assertCompletionContainsSnippet(t, lexicalItemsAtMarker(t, server, document, "/*explicit*/"), "div", "<div>${1}</div>")
	assertCompletionContainsSnippet(t, lexicalItemsAtMarker(t, server, document, "/*short*/"), "section", "<section>${1}</section>")
	assertCompletionContainsSnippet(t, lexicalItemsAtMarker(t, server, document, "/*argument*/"), "span", "<span>${1}</span>")
}

func TestTemplateCompletionRejectsGoComparisonRHS(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Build() {
	valid := value == di/*cursor*/
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	assertCompletionMissing(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "div")
}

func TestIfCompletionDependsOnGoOrTemplateContext(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Card() string {
	if/*go*/
	return <main>if/*template*/</main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	goItems := lexicalItemsAtMarker(t, server, document, "/*go*/")
	assertCompletionContainsSnippet(t, goItems, "if", "if ${1:condition} {\n\t${2}\n}")
	templateItems := lexicalItemsAtMarker(t, server, document, "/*template*/")
	assertCompletionContainsSnippet(t, templateItems, "if", "<:if condition={{ ${1} }}>\n\t${2}\n</:if>")
}

func TestTemplateCompletionDoesNotRunInsideGoExpression(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Card() string {
	div := "value"
	return <main>{{ di/*cursor*/ }}</main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	assertCompletionLabels(t, items, "div")
	if items[0].InsertTextFormat != 0 {
		t.Fatalf("Go expression completion became a snippet: %#v", items[0])
	}
}

func TestTemplateCompletionCreatesDynamicCustomElementSnippet(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Card() string {
	return <main>mi-tag-custom/*cursor*/</main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	for _, item := range items {
		if item.Label != "mi-tag-custom" {
			continue
		}
		if item.InsertTextFormat != 2 || item.TextEdit == nil || item.TextEdit.NewText != "<mi-tag-custom>${1}</mi-tag-custom>" {
			t.Fatalf("custom element completion = %#v", item)
		}
		start := strings.Index(pageSource, "mi-tag-custom")
		wantStart, err := document.Position(start)
		if err != nil {
			t.Fatal(err)
		}
		wantEnd, err := document.Position(start + len("mi-tag-custom"))
		if err != nil {
			t.Fatal(err)
		}
		if item.TextEdit.Range != (Range{Start: wantStart, End: wantEnd}) {
			t.Fatalf("custom element edit range = %#v, want %#v", item.TextEdit.Range, Range{Start: wantStart, End: wantEnd})
		}
		return
	}
	t.Fatalf("custom element completion not found: %#v", items)
}

func TestCustomElementCompletionIsTemplateOnlyAndRejectsReservedNames(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	goSource := `module page
func Build() {
	mi-tag-custom/*cursor*/
}
`
	server := NewServer()
	goDocument := Document{URI: FileURI(pagePath), Path: pagePath, Text: goSource}
	assertCompletionMissing(t, lexicalItemsAtMarker(t, server, goDocument, "/*cursor*/"), "mi-tag-custom")

	templateSource := `module page
func Card() string {
	return <main>font-face/*cursor*/</main>
}
`
	templateDocument := Document{URI: FileURI(pagePath), Path: pagePath, Text: templateSource}
	assertCompletionMissing(t, lexicalItemsAtMarker(t, server, templateDocument, "/*cursor*/"), "font-face")
}

func assertCompletionContainsSnippet(t *testing.T, items []CompletionItem, label, snippet string) {
	t.Helper()
	for _, item := range items {
		if item.Label != label {
			continue
		}
		if item.InsertText != snippet || item.InsertTextFormat != 2 {
			t.Fatalf("completion %q = %#v, want snippet %q", label, item, snippet)
		}
		return
	}
	t.Fatalf("completion %q not found in %#v", label, items)
}

func TestGoCompletionCreatesSafeGoroutineSnippet(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
func Build() {
	go/*cursor*/
}
`
	server := NewServer()
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	want := "go func() {\n\tdefer func() {\n\t\tif err := recover(); err != nil {\n\t\t\t${1:// handle panic}\n\t\t}\n\t}()\n\n\t${2}\n}()"
	assertCompletionContainsSnippet(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "go", want)
}

func TestSafeGoroutineSnippetSatisfiesCompilerSafetyRule(t *testing.T) {
	snippets := goStatementSnippetCompletions("go")
	if len(snippets) != 1 || snippets[0].Label != "go" {
		t.Fatalf("go snippets = %#v, want one go completion", snippets)
	}
	body := strings.ReplaceAll(snippets[0].InsertText, "${1:// handle panic}", "// handle panic")
	body = strings.ReplaceAll(body, "${2}", "work()")
	source := "module page\nfunc work() {}\nfunc Build() {\n" + body + "\n}\n"
	if _, err := goxcompiler.ParseModule("page.gox", source); err != nil {
		t.Fatalf("safe goroutine snippet rejected by compiler: %v", err)
	}
}

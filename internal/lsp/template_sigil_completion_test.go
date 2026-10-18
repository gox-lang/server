package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestComponentSigilCompletionUsesParametersAndChildren(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
import gox "github.com/gox-lang/gox"
func Card(name string, count int, children gox.Component) gox.Component {
	return <article><&children /></article>
}
func Render() gox.Component {
	return <main>&Ca/*cursor*/</main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(path), Path: path, Text: source}
	item := requireSigilCompletion(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "&Card")
	want := `<&Card name={{ ${1} }} count={{ ${2} }}>${3}</&Card>`
	if item.TextEdit == nil || item.TextEdit.NewText != want || item.InsertTextFormat != 2 {
		t.Fatalf("Card completion = %#v, want %q", item, want)
	}
}

func TestComponentSigilCompletionIncludesTemplateVariables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
import gox "github.com/gox-lang/gox"
func Render() gox.Component {
	var explicit gox.Component = <h1>Explicit</h1>
	short := <h2>Short</h2>
	return <main>&/*cursor*/</main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(path), Path: path, Text: source}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	for _, label := range []string{"&explicit", "&short"} {
		item := requireSigilCompletion(t, items, label)
		if item.TextEdit == nil || item.TextEdit.NewText != "<"+label+" />" {
			t.Fatalf("%s completion = %#v", label, item)
		}
	}
}

func TestComponentSigilCompletionIncludesImportedExportedComponents(t *testing.T) {
	root := t.TempDir()
	cardPath := filepath.Join(root, "card.gox")
	pagePath := filepath.Join(root, "page.gox")
	cardSource := `module card
import gox "github.com/gox-lang/gox"
func Card(name string) gox.Component { return <article>{{ name }}</article> }
func private() gox.Component { return <span>Private</span> }
`
	pageSource := `module page
import (
	card "card.gox"
	gox "github.com/gox-lang/gox"
)
func Render() gox.Component { return <main>&card./*cursor*/</main> }
`
	server := NewServer()
	writeWorkspaceGOX(t, cardPath, cardSource)
	cardDocument := Document{URI: FileURI(cardPath), Path: cardPath, Text: cardSource}
	pageDocument := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	if err := server.symbols.UpdateDisk(cardDocument); err != nil {
		t.Fatal(err)
	}
	item := requireSigilCompletion(t, lexicalItemsAtMarker(t, server, pageDocument, "/*cursor*/"), "&card.Card")
	if item.TextEdit == nil || item.TextEdit.NewText != `<&card.Card name={{ ${1} }} />` {
		t.Fatalf("imported Card completion = %#v", item)
	}
}

func TestComponentSigilCompletionRepairsExistingQualifiedTagName(t *testing.T) {
	root := t.TempDir()
	cardPath := filepath.Join(root, "card.gox")
	pagePath := filepath.Join(root, "page.gox")
	cardSource := `module card
import gox "github.com/gox-lang/gox"
func Cardes(name string) gox.Component { return <article>{{ name }}</article> }
`
	pageSource := `module page
import (
	card "card.gox"
	gox "github.com/gox-lang/gox"
)
func Render(user string) gox.Component {
	return <main><&card.Card name={{ user }} /></main>
}
`
	server := NewServer()
	writeWorkspaceGOX(t, cardPath, cardSource)
	if err := server.symbols.UpdateDisk(Document{URI: FileURI(cardPath), Path: cardPath, Text: cardSource}); err != nil {
		t.Fatal(err)
	}
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	offset := strings.Index(pageSource, "card.Card") + len("card.Card")
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	item := requireSigilCompletion(t, server.completions(document, position).Items, "&card.Cardes")
	if item.TextEdit == nil || item.TextEdit.NewText != `<&card.Cardes` || item.FilterText != `<&card.Cardes` {
		t.Fatalf("qualified tag repair completion = %#v", item)
	}
	startOffset, err := document.Offset(item.TextEdit.Range.Start)
	if err != nil {
		t.Fatal(err)
	}
	endOffset, err := document.Offset(item.TextEdit.Range.End)
	if err != nil {
		t.Fatal(err)
	}
	if startOffset != strings.Index(pageSource, "<&card.Card") || endOffset != offset {
		t.Fatalf("qualified tag repair range = %d:%d", startOffset, endOffset)
	}
}

func TestClientSigilCompletionIncludesHydrateModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
client Menu react {
	src: "menu/menu.jsx"
	dev: true
}
func Render() string { return <main>@Me/*cursor*/</main> }
`
	server := NewServer()
	document := Document{URI: FileURI(path), Path: path, Text: source}
	item := requireSigilCompletion(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "@Menu")
	want := `<@Menu hydrate="${1|load,idle,visible,hover,interaction|}" />`
	if item.TextEdit == nil || item.TextEdit.NewText != want {
		t.Fatalf("Menu completion = %#v, want %q", item, want)
	}
}

func TestDirectiveSigilCompletionRespectsContainer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
func Render() string {
	return <main>
		:/*root*/
		<:for index user range={{ users }}>:/*for*/</:for>
		<:switch value={{ mode }}>:/*switch*/</:switch>
		<:if-group>:/*group*/</:if-group>
	</main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(path), Path: path, Text: source}
	rootItems := lexicalItemsAtMarker(t, server, document, "/*root*/")
	requireSigilCompletion(t, rootItems, ":if")
	assertCompletionMissing(t, rootItems, ":empty")
	forItems := lexicalItemsAtMarker(t, server, document, "/*for*/")
	requireSigilCompletion(t, forItems, ":empty")
	requireSigilCompletion(t, forItems, ":each")
	requireSigilCompletion(t, forItems, ":break")
	switchItems := lexicalItemsAtMarker(t, server, document, "/*switch*/")
	requireSigilCompletion(t, switchItems, ":case")
	requireSigilCompletion(t, switchItems, ":default")
	assertCompletionMissing(t, switchItems, ":for")
	groupItems := lexicalItemsAtMarker(t, server, document, "/*group*/")
	requireSigilCompletion(t, groupItems, ":else-if")
	requireSigilCompletion(t, groupItems, ":else")
}

func TestSigilCompletionIsTemplateOnlyAndReplacesOptionalBracket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	goSource := "module page\nfunc Build() { &Ca/*cursor*/ }\n"
	server := NewServer()
	goDocument := Document{URI: FileURI(path), Path: path, Text: goSource}
	if items := lexicalItemsAtMarker(t, server, goDocument, "/*cursor*/"); len(items) != 0 {
		t.Fatalf("Go sigil completions = %#v", items)
	}
	templateSource := `module page
import gox "github.com/gox-lang/gox"
func Card() gox.Component { return <span>Card</span> }
func Render() gox.Component { return <&Ca/*cursor*/ }
`
	templateDocument := Document{URI: FileURI(path), Path: path, Text: templateSource}
	item := requireSigilCompletion(t, lexicalItemsAtMarker(t, server, templateDocument, "/*cursor*/"), "&Card")
	if item.TextEdit == nil || item.TextEdit.NewText != `<&Card />` {
		t.Fatalf("bracket component completion = %#v", item)
	}
	start := strings.Index(templateSource, "<&Ca")
	wantStart, err := templateDocument.Position(start)
	if err != nil {
		t.Fatal(err)
	}
	if item.TextEdit.Range.Start != wantStart {
		t.Fatalf("replacement starts at %#v, want %#v", item.TextEdit.Range.Start, wantStart)
	}
}

func requireSigilCompletion(t *testing.T, items []CompletionItem, label string) CompletionItem {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("completion %q not found in %#v", label, items)
	return CompletionItem{}
}

func TestIfAssignSigilSnippets(t *testing.T) {
	for _, prefix := range []string{":", "<:", ":if-a", "<:if-a"} {
		source := "module page\nfunc Render() gox.Component {return <main>" + prefix + "/*cursor*/</main>}"
		doc := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
		item := requireSigilCompletion(t, lexicalItemsAtMarker(t, NewServer(), doc, "/*cursor*/"), ":if-assign")
		want := "<:if ${1} :assign={{ ${2} }} condition={{ ${3} }}>\n\t$0\n</:if>"
		if item.TextEdit == nil || item.TextEdit.NewText != want || item.InsertTextFormat != 2 {
			t.Fatalf("unexpected snippet: %#v", item)
		}
		start, _ := doc.Offset(item.TextEdit.Range.Start)
		end, _ := doc.Offset(item.TextEdit.Range.End)
		if doc.Text[start:end] != prefix {
			t.Fatalf("replacement %q, want %q", doc.Text[start:end], prefix)
		}
	}
	source := "module page\nfunc Render() gox.Component {return <:if-group>:/*cursor*/</:if-group>}"
	doc := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	items := lexicalItemsAtMarker(t, NewServer(), doc, "/*cursor*/")
	requireSigilCompletion(t, items, ":if-assign")
	item := requireSigilCompletion(t, items, ":else-if-assign")
	if item.TextEdit == nil || !strings.HasPrefix(item.TextEdit.NewText, "<:else-if ${1} :assign=") {
		t.Fatalf("unexpected else-if snippet: %#v", item)
	}
}

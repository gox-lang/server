package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestComponentAttributeCompletionUsesParametersAndOmitsExistingAndChildren(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	indexedSource := `module page
import gox "github.com/gox-lang/gox"
func Card(name string, count int, active bool, children gox.Component) gox.Component {
	return <article><&children /></article>
}
func Render() gox.Component { return <&Card name="George" count={{ 1 }} active={{ true }} /> }
`
	liveSource := strings.Replace(indexedSource, `active={{ true }}`, `ac/*cursor*/`, 1)
	server := NewServer()
	indexCompletionDocument(t, server, path, indexedSource)
	document := Document{URI: FileURI(path), Path: path, Text: liveSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	active := requireCompletionItem(t, items, "active")
	if active.TextEdit == nil || active.TextEdit.NewText != "active={{ ${1} }}" {
		t.Fatalf("active completion = %#v", active)
	}
	assertCompletionMissing(t, items, "name")
	assertCompletionMissing(t, items, "count")
	assertCompletionMissing(t, items, "children")
}

func TestImportedComponentAttributeCompletionUsesExportedSignature(t *testing.T) {
	root := t.TempDir()
	cardPath := filepath.Join(root, "card.gox")
	pagePath := filepath.Join(root, "page.gox")
	cardSource := `module card
import gox "github.com/gox-lang/gox"
func Card(name string, count int) gox.Component { return <article>{{ name }}</article> }
`
	pageSource := `module page
import card "card.gox"
func Render() string { return <&card.Card co/*cursor*/ /> }
`
	server := NewServer()
	writeWorkspaceGOX(t, cardPath, cardSource)
	writeWorkspaceGOX(t, pagePath, strings.Replace(pageSource, " co/*cursor*/", "", 1))
	indexCompletionDocument(t, server, cardPath, cardSource)
	indexCompletionDocument(t, server, pagePath, strings.Replace(pageSource, " co/*cursor*/", "", 1))
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "count")
	if item.TextEdit == nil || item.TextEdit.NewText != "count={{ ${1} }}" {
		t.Fatalf("imported component attribute = %#v", item)
	}
}

func TestClientAttributeCompletionUsesAllGOXControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	indexedSource := `module page
client Menu react {
	src: "menu/menu.jsx"
	dev: true
}
func Render() string { return <@Menu /> }
`
	liveSource := strings.Replace(indexedSource, "<@Menu />", `<@Menu hydrate="visible" /*cursor*/ />`, 1)
	server := NewServer()
	indexCompletionDocument(t, server, path, indexedSource)
	document := Document{URI: FileURI(path), Path: path, Text: liveSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	for _, label := range []string{"isolate", "transition", ":spread"} {
		requireCompletionItem(t, items, label)
	}
	assertCompletionMissing(t, items, "hydrate")
	for _, internal := range []string{"data-props", "data-script", "data-assets", "data-hydrate", "data-transition", "data-isolate"} {
		assertCompletionMissing(t, items, internal)
	}
}

func TestClientAttributeValueCompletionProvidesValidatedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	indexedSource := `module page
client Menu react { src: "menu/menu.jsx" }
func Render() string { return <@Menu /> }
`
	server := NewServer()
	indexCompletionDocument(t, server, path, indexedSource)
	for _, test := range []struct {
		markup string
		want   []string
	}{
		{`<@Menu hydrate="vi/*cursor*/" />`, []string{"visible"}},
		{`<@Menu isolate="/*cursor*/" />`, []string{"true", "false"}},
		{`<@Menu adapt="/*cursor*/" />`, []string{"top", "bottom", "left", "right", "vertical-center", "horizontal-center", "top-left", "top-right", "bottom-left", "bottom-right", "center", "none"}},
	} {
		live := strings.Replace(indexedSource, "<@Menu />", test.markup, 1)
		document := Document{URI: FileURI(path), Path: path, Text: live}
		items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
		assertCompletionLabels(t, items, test.want...)
	}
}

func TestDirectiveAttributeCompletionFollowsCompilerRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
func Render() string {
	return <main>
		<:if con/*if*/></:if>
		<:for index item ra/*for*/></:for>
		<:repeat ti/*repeat*/></:repeat>
		<:switch va/*switch*/><:case is={{ 1 }} fall/*case*/></:case></:switch>
	</main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(path), Path: path, Text: source}
	for marker, label := range map[string]string{"/*if*/": "condition", "/*for*/": "range", "/*repeat*/": "times", "/*switch*/": "value", "/*case*/": "fallthrough"} {
		requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, marker), label)
	}
}

func TestHTMLAttributeCompletionProvidesNamesValuesAndNoDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
func Render() string {
	return <main>
		<input name="user" ty/*name*/>
		<input type="e/*value*/">
		<a target="_blank" /*duplicate*/></a>
	</main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(path), Path: path, Text: source}
	nameItems := lexicalItemsAtMarker(t, server, document, "/*name*/")
	requireCompletionItem(t, nameItems, "type")
	assertCompletionMissing(t, nameItems, "name")
	valueItems := lexicalItemsAtMarker(t, server, document, "/*value*/")
	assertCompletionLabels(t, valueItems, "email")
	duplicateItems := lexicalItemsAtMarker(t, server, document, "/*duplicate*/")
	assertCompletionMissing(t, duplicateItems, "target")
	requireCompletionItem(t, duplicateItems, "href")
	requireCompletionItem(t, duplicateItems, ":spread")
}

func indexCompletionDocument(t *testing.T, server *Server, path, source string) {
	t.Helper()
	if err := server.symbols.UpdateDisk(Document{URI: FileURI(path), Path: path, Text: source}); err != nil {
		t.Fatal(err)
	}
}

func requireCompletionItem(t *testing.T, items []CompletionItem, label string) CompletionItem {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("completion %q not found in %#v", label, items)
	return CompletionItem{}
}

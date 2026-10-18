package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOrdinaryCompletionExcludesComponentsInGoAndTemplateExpressions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
import gox "github.com/gox-lang/gox"
func Helper() string { return "George" }
func Card(name string) gox.Component { return <article>{{ name }}</article> }
func Render() gox.Component {
	var explicit gox.Component = <h1>Explicit</h1>
	short := <h2>Short</h2>
	normalValue := "George"
	/*go*/
	return <main>{{ /*expression*/ }}</main>
}
`
	replacements := map[string]string{"/*expression*/": "normalValue"}
	server := NewServer()
	indexCompletionDocument(t, server, path, replaceCompletionMarkers(source, replacements, ""))

	goDocument := expressionCompletionDocument(path, source, replacements, "/*go*/")
	assertOrdinaryCompletionBoundary(t, lexicalItemsAtMarker(t, server, goDocument, "/*go*/"))
	expressionDocument := expressionCompletionDocument(path, source, replacements, "/*expression*/")
	assertOrdinaryCompletionBoundary(t, lexicalItemsAtMarker(t, server, expressionDocument, "/*expression*/"))
}

func TestComponentSigilCompletionKeepsFunctionsAndInlineComponents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
import gox "github.com/gox-lang/gox"
func Card() gox.Component { return <article></article> }
func Render() gox.Component {
	var explicit gox.Component = <h1>Explicit</h1>
	short := <h2>Short</h2>
	return <main>&/*cursor*/</main>
}
`
	server := NewServer()
	document := Document{URI: FileURI(path), Path: path, Text: source}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	for _, label := range []string{"&Card", "&explicit", "&short"} {
		requireCompletionItem(t, items, label)
	}
}

func TestImportedModuleCompletionExcludesExportedComponents(t *testing.T) {
	root := t.TempDir()
	widgetsPath := filepath.Join(root, "widgets.gox")
	pagePath := filepath.Join(root, "page.gox")
	widgetsSource := `module widgets
import gox "github.com/gox-lang/gox"
func Card() gox.Component { return <article></article> }
func Helper() string { return "George" }
`
	indexedPage := `module page
import widgets "widgets.gox"
func Render() string { return <span>{{ widgets.Helper() }}</span> }
`
	livePage := strings.Replace(indexedPage, "widgets.Helper()", "widgets./*cursor*/", 1)
	server := NewServer()
	writeWorkspaceGOX(t, widgetsPath, widgetsSource)
	writeWorkspaceGOX(t, pagePath, indexedPage)
	indexCompletionDocument(t, server, widgetsPath, widgetsSource)
	indexCompletionDocument(t, server, pagePath, indexedPage)
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: livePage}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	requireCompletionItem(t, items, "Helper")
	assertCompletionMissing(t, items, "Card")
}

func TestMemberCompletionExcludesMethodsReturningComponents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	indexedSource := `module page
import gox "github.com/gox-lang/gox"
type Builder struct{}
func (Builder) Component() gox.Component { return <span></span> }
func (Builder) Name() string { return "George" }
func Render() string { builder := Builder{}; return <span>{{ builder.Name() }}</span> }
`
	liveSource := strings.Replace(indexedSource, "builder.Name()", "builder./*cursor*/", 1)
	server := NewServer()
	indexCompletionDocument(t, server, path, indexedSource)
	document := Document{URI: FileURI(path), Path: path, Text: liveSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	requireCompletionItem(t, items, "Name")
	assertCompletionMissing(t, items, "Component")
}

func assertOrdinaryCompletionBoundary(t *testing.T, items []CompletionItem) {
	t.Helper()
	for _, label := range []string{"Helper", "normalValue"} {
		requireCompletionItem(t, items, label)
	}
	for _, label := range []string{"Card", "explicit", "short"} {
		assertCompletionMissing(t, items, label)
	}
}

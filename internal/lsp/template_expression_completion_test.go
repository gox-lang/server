package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateExpressionCompletionRanksComponentParameterTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
import gox "github.com/gox-lang/gox"
func Ready() bool { return true }
func Title() string { return "George" }
func Lookup() (bool, error) { return true, nil }
func Card(name string, active bool, count int) gox.Component { return <article></article> }
func Render() gox.Component {
	enabled := true
	title := "George"
	total := 3
	return <&Card active={{ /*bool*/ }} name={{ /*string*/ }} count={{ /*int*/ }} />
}
`
	server := NewServer()
	replacements := map[string]string{"/*bool*/": "enabled", "/*string*/": "title", "/*int*/": "total"}
	indexCompletionDocument(t, server, path, replaceCompletionMarkers(source, replacements, ""))

	document := expressionCompletionDocument(path, source, replacements, "/*bool*/")
	boolItems := lexicalItemsAtMarker(t, server, document, "/*bool*/")
	assertCompletionBefore(t, boolItems, "enabled", "title")
	assertCompletionBefore(t, boolItems, "Ready", "Title")
	assertCompletionBefore(t, boolItems, "true", "total")
	ready := requireCompletionItem(t, boolItems, "Ready")
	if ready.InsertText != "Ready()$0" || ready.InsertTextFormat != 2 {
		t.Fatalf("Ready completion = %#v", ready)
	}
	lookup := requireCompletionItem(t, boolItems, "Lookup")
	if lookup.InsertText == "Lookup()" {
		t.Fatalf("multiple-result function became a value snippet: %#v", lookup)
	}

	document = expressionCompletionDocument(path, source, replacements, "/*string*/")
	stringItems := lexicalItemsAtMarker(t, server, document, "/*string*/")
	assertCompletionBefore(t, stringItems, "title", "enabled")
	assertCompletionBefore(t, stringItems, "Title", "Ready")
	requireCompletionItem(t, stringItems, `""`)

	document = expressionCompletionDocument(path, source, replacements, "/*int*/")
	integerItems := lexicalItemsAtMarker(t, server, document, "/*int*/")
	assertCompletionBefore(t, integerItems, "total", "title")
	requireCompletionItem(t, integerItems, "0")
}

func TestTemplateExpressionCompletionUsesDirectiveAndClientContracts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
client Menu react { src: "menu.jsx" }
func Render() string {
	enabled := true
	count := 3
	title := "ready"
	users := []string{"George"}
	return <main>
		<:if condition={{ /*condition*/ }}></:if>
		<:repeat times={{ /*times*/ }}></:repeat>
		<:for _ user range={{ /*range*/ }}></:for>
		<@Menu isolate={{ /*isolate*/ }} transition={{ /*transition*/ }} />
	</main>
}
`
	server := NewServer()
	replacements := map[string]string{
		"/*condition*/": "enabled", "/*times*/": "count", "/*range*/": "users",
		"/*isolate*/": "enabled", "/*transition*/": "title",
	}
	indexCompletionDocument(t, server, path, replaceCompletionMarkers(source, replacements, ""))
	items := func(marker string) []CompletionItem {
		return lexicalItemsAtMarker(t, server, expressionCompletionDocument(path, source, replacements, marker), marker)
	}
	assertCompletionBefore(t, items("/*condition*/"), "enabled", "title")
	assertCompletionBefore(t, items("/*times*/"), "count", "enabled")
	assertCompletionBefore(t, items("/*range*/"), "users", "count")
	assertCompletionBefore(t, items("/*isolate*/"), "enabled", "title")
	assertCompletionBefore(t, items("/*transition*/"), "title", "enabled")
}

func TestTemplateExpressionCompletionUsesEnclosingSwitchType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
func Render() string {
	status := "ready"
	ready := "ready"
	code := 1
	return <:switch value={{ status }}><:case is={{ /*case*/ }}></:case></:switch>
}
`
	server := NewServer()
	indexCompletionDocument(t, server, path, strings.Replace(source, "/*case*/", "ready", 1))
	document := Document{URI: FileURI(path), Path: path, Text: source}
	assertCompletionBefore(t, lexicalItemsAtMarker(t, server, document, "/*case*/"), "ready", "code")
}

func TestTemplateExpressionCompletionUsesDefinedTypesAliasesAndConstants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
import gox "github.com/gox-lang/gox"
type Status string
type Label = string
const StatusReady Status = "ready"
const StatusLoading Status = "loading"
func Card(status Status, label Label) gox.Component { return <span></span> }
func Render() gox.Component {
	title := "George"
	enabled := true
	return <&Card status={{ /*status*/ }} label={{ /*label*/ }} />
}

`
	replacements := map[string]string{"/*status*/": "StatusReady", "/*label*/": "title"}
	server := NewServer()
	indexCompletionDocument(t, server, path, replaceCompletionMarkers(source, replacements, ""))

	statusItems := lexicalItemsAtMarker(t, server, expressionCompletionDocument(path, source, replacements, "/*status*/"), "/*status*/")
	assertCompletionBefore(t, statusItems, "StatusReady", "title")
	assertCompletionBefore(t, statusItems, "StatusLoading", "enabled")
	requireCompletionItem(t, statusItems, `""`)

	aliasItems := lexicalItemsAtMarker(t, server, expressionCompletionDocument(path, source, replacements, "/*label*/"), "/*label*/")
	assertCompletionBefore(t, aliasItems, "title", "enabled")
}

func TestTemplateCaseCompletionPrioritizesConstantsOfSwitchDefinedType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
type Status string
const StatusReady Status = "ready"
const StatusLoading Status = "loading"
const Other = 1
func Render() string {
	var status Status = StatusReady
	return <:switch value={{ status }}><:case is={{ /*case*/ }}></:case></:switch>
}
`
	server := NewServer()
	indexCompletionDocument(t, server, path, strings.Replace(source, "/*case*/", "StatusReady", 1))
	document := Document{URI: FileURI(path), Path: path, Text: source}
	items := lexicalItemsAtMarker(t, server, document, "/*case*/")
	assertCompletionBefore(t, items, "StatusReady", "Other")
	assertCompletionBefore(t, items, "StatusLoading", "Other")
}

func TestTemplateExpressionCompletionQualifiesImportedDefinedTypes(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	usersSource := `module users
import gox "github.com/gox-lang/gox"
type Status string
const StatusReady Status = "ready"
const Other = 1
func Badge(status Status) gox.Component { return <span></span> }
`
	pageSource := `module page
import users "users.gox"
import gox "github.com/gox-lang/gox"
func Render() gox.Component { return <&users.Badge status={{ users./*cursor*/ }} /> }
`
	server := NewServer()
	writeWorkspaceGOX(t, usersPath, usersSource)
	writeWorkspaceGOX(t, pagePath, strings.Replace(pageSource, "users./*cursor*/", "users.StatusReady", 1))
	indexCompletionDocument(t, server, usersPath, usersSource)
	indexCompletionDocument(t, server, pagePath, strings.Replace(pageSource, "users./*cursor*/", "users.StatusReady", 1))
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	assertCompletionBefore(t, items, "StatusReady", "Other")
}

func TestTemplateExpressionCompletionSuggestsContractValuesAsGoStrings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
client Menu react { src: "menu.jsx" }
func Render() string {
	return <main>
		<@Menu hydrate={{ /*hydrate*/ }} transition={{ /*transition*/ }} />
		<form method={{ /*method*/ }}></form>
		<img loading={{ /*loading*/ }} crossorigin={{ /*cors*/ }}>
	</main>
}
`
	replacements := map[string]string{
		"/*hydrate*/": `"load"`, "/*transition*/": `"fade"`, "/*method*/": `"post"`,
		"/*loading*/": `"lazy"`, "/*cors*/": `"anonymous"`,
	}
	server := NewServer()
	indexCompletionDocument(t, server, path, replaceCompletionMarkers(source, replacements, ""))
	items := func(marker string) []CompletionItem {
		return lexicalItemsAtMarker(t, server, expressionCompletionDocument(path, source, replacements, marker), marker)
	}
	for marker, labels := range map[string][]string{
		"/*hydrate*/":    {`"load"`, `"visible"`, `"interaction"`},
		"/*transition*/": {`"fade"`},
		"/*method*/":     {`"get"`, `"post"`, `"dialog"`},
		"/*loading*/":    {`"eager"`, `"lazy"`},
		"/*cors*/":       {`"anonymous"`, `"use-credentials"`},
	} {
		completionItems := items(marker)
		for _, label := range labels {
			item := requireCompletionItem(t, completionItems, label)
			if item.InsertText != label {
				t.Fatalf("%s completion = %#v", label, item)
			}
		}
	}
}

func TestTemplateExpressionCompletionUsesGoPackageDefinedTypes(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "status", "status.go"), `package status
type Status string
const Ready Status = "ready"
const Other = 1
`)
	pagePath := filepath.Join(root, "page.gox")
	indexedSource := `module page
import status "example/status"
import gox "github.com/gox-lang/gox"
func Badge(value status.Status) gox.Component { return <span></span> }
func Render() gox.Component { return <&Badge value={{ status.Ready }} /> }
`
	liveSource := strings.Replace(indexedSource, "status.Ready", "status./*cursor*/", 1)
	writeWorkspaceGOX(t, pagePath, indexedSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: liveSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	assertCompletionBefore(t, items, "Ready", "Other")
}

func expressionCompletionDocument(path, source string, replacements map[string]string, active string) Document {
	return Document{URI: FileURI(path), Path: path, Text: replaceCompletionMarkers(source, replacements, active)}
}

func replaceCompletionMarkers(source string, replacements map[string]string, active string) string {
	for marker, value := range replacements {
		if marker != active {
			source = strings.ReplaceAll(source, marker, value)
		}
	}
	return source
}

func assertCompletionBefore(t *testing.T, items []CompletionItem, first, second string) {
	t.Helper()
	firstIndex, secondIndex := -1, -1
	for index, item := range items {
		if item.Label == first && firstIndex < 0 {
			firstIndex = index
		}
		if item.Label == second && secondIndex < 0 {
			secondIndex = index
		}
	}
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Fatalf("completion %q (%d) must precede %q (%d): %#v", first, firstIndex, second, secondIndex, items)
	}
}

package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestImportPathCompletionSuggestsGoxModulesAndGoPackages(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "library", "api.go"), "package library\nfunc Open() {}\n")
	writeWorkspaceGOX(t, filepath.Join(root, "pages", "shared.menu.gox"), "module menu\nfunc Menu() string { return \"menu\" }\n")
	pagePath := filepath.Join(root, "pages", "index.gox")
	pageSource := "module page\nimport menu \"sha/*cursor*/\"\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(pageSource, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	item := requireCompletionItem(t, items, "shared.menu.gox")
	if item.TextEdit == nil || item.TextEdit.NewText != "shared.menu.gox" {
		t.Fatalf("GOX import completion = %#v", item)
	}

	document.Text = "module page\nimport library \"example/lib/*cursor*/\"\n"
	items = lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	requireCompletionItem(t, items, "example/library")
}

func TestImportPathCompletionSuggestsImplicitGoxImportInsideString(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeWorkspaceGOX(t, filepath.Join(root, "pages", "@menu.gox"), "module menu\nfunc Menu() string { return \"menu\" }\n")
	pagePath := filepath.Join(root, "pages", "account", "index.gox")
	pageSource := "module page\nimport (\n\t\"@/*cursor*/\"\n)\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(pageSource, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	list := completionListAtMarkerWithoutMarker(t, server, document, "/*cursor*/")
	if !list.IsIncomplete {
		t.Fatal("import-path completion list must remain incomplete while typing")
	}
	item := requireCompletionItem(t, list.Items, "@menu.gox")
	if item.TextEdit == nil || item.TextEdit.NewText != "@menu.gox" || len(item.AdditionalTextEdits) != 1 || item.AdditionalTextEdits[0].NewText != "menu " {
		t.Fatalf("implicit GOX string import completion = %#v", item)
	}
}

func TestImportPathCompletionFindsGoPackagesByPathAndAddsDeclaredAlias(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), "package register\nfunc Create() {}\n")
	pagePath := filepath.Join(root, "pages", "index.gox")
	pageSource := `module page
import (
	menu "@menu.gox"
	gox "github.com/gox-lang/gox"
	"bil/*cursor*/"
)
`
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(pageSource, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	item := requireCompletionItem(t, completionItemsAtMarkerWithoutMarker(t, server, document, "/*cursor*/"), "billing")
	if item.TextEdit == nil || item.TextEdit.NewText != "gox-editor-test/billing" || len(item.AdditionalTextEdits) != 1 || item.AdditionalTextEdits[0].NewText != "register " {
		t.Fatalf("path completion = %#v", item)
	}
	if item.TextEdit.Range.Start != (Position{Line: 4, Character: 2}) || item.TextEdit.Range.End != (Position{Line: 4, Character: 5}) ||
		item.AdditionalTextEdits[0].Range != (Range{Start: Position{Line: 4, Character: 1}, End: Position{Line: 4, Character: 1}}) {
		t.Fatalf("path completion edits must preserve the quoted filter range: %#v", item)
	}
	if item.FilterText != "billing" {
		t.Fatalf("path completion filter = %#v", item)
	}
}

func TestImportPathCompletionReplacesBareImportEntryWithDeclaredAlias(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), "package register\nfunc Create() {}\n")
	pagePath := filepath.Join(root, "pages", "index.gox")
	pageSource := `module page
import (
	menu "@menu.gox"
	gox "github.com/gox-lang/gox"
	bil/*cursor*/
)
`
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(pageSource, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	item := requireCompletionItem(t, completionItemsAtMarkerWithoutMarker(t, server, document, "/*cursor*/"), "billing")
	if item.TextEdit == nil || item.TextEdit.NewText != `register "gox-editor-test/billing"` {
		t.Fatalf("bare import completion = %#v", item)
	}
	if item.TextEdit.Range.Start != (Position{Line: 4, Character: 1}) || item.TextEdit.Range.End != (Position{Line: 4, Character: 4}) {
		t.Fatalf("bare import replacement range = %#v", item.TextEdit.Range)
	}
	if item.FilterText != "billing" {
		t.Fatalf("bare path completion filter = %#v", item)
	}
}

func TestBareImportPathCompletionAlsoMatchesDeclaredPackageName(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), "package register\nfunc Create() {}\n")
	pagePath := filepath.Join(root, "pages", "index.gox")
	pageSource := "module page\nimport (\n\treg/*cursor*/\n)\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(pageSource, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	item := requireCompletionItem(t, completionItemsAtMarkerWithoutMarker(t, server, document, "/*cursor*/"), "register")
	if item.TextEdit == nil || item.TextEdit.NewText != `register "gox-editor-test/billing"` {
		t.Fatalf("bare package-name import completion = %#v", item)
	}
	if item.FilterText != "register" {
		t.Fatalf("bare package-name completion filter = %#v", item)
	}
}

func TestImportPathCompletionPreservesExplicitAlias(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), "package register\nfunc Create() {}\n")
	pagePath := filepath.Join(root, "pages", "index.gox")
	pageSource := "module page\nimport custom \"bil/*cursor*/\"\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(pageSource, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "billing")
	if item.TextEdit == nil || item.TextEdit.NewText != "gox-editor-test/billing" {
		t.Fatalf("explicit alias completion = %#v", item)
	}
}

func completionItemsAtMarkerWithoutMarker(t *testing.T, server *Server, document Document, marker string) []CompletionItem {
	return completionListAtMarkerWithoutMarker(t, server, document, marker).Items
}

func completionListAtMarkerWithoutMarker(t *testing.T, server *Server, document Document, marker string) CompletionList {
	t.Helper()
	offset := strings.Index(document.Text, marker)
	if offset < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	document.Text = strings.Replace(document.Text, marker, "", 1)
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	return server.completions(document, position)
}

func TestImportPathCompletionUsesNearestAncestralAlias(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	parent := filepath.Join(root, "pages", "@menu.gox")
	nearest := filepath.Join(root, "pages", "account", "@menu.gox")
	writeWorkspaceGOX(t, parent, "module parentmenu\nfunc Menu() string { return \"parent\" }\n")
	writeWorkspaceGOX(t, nearest, "module menu\nfunc Menu() string { return \"nearest\" }\n")
	writeWorkspaceGOX(t, filepath.Join(root, "pages", "@parts", "item.gox"), "module item\nfunc Item() string { return \"item\" }\n")
	pagePath := filepath.Join(root, "pages", "account", "settings", "index.gox")
	pageSource := "module page\nimport menu \"@/*cursor*/\"\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(pageSource, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	menu := requireCompletionItem(t, items, "@menu.gox")
	if menu.Detail != "GOX ancestral alias → "+filepath.ToSlash(nearest) {
		t.Fatalf("nearest alias completion = %#v", menu)
	}
	requireCompletionItem(t, items, "@parts/item.gox")
}

func TestCompletionDeduplicatesIdenticalAncestralComponentCandidate(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	menuPath := filepath.Join(root, "pages", "@menu.gox")
	writeWorkspaceGOX(t, menuPath, `module menu
import gox "github.com/gox-lang/gox"
func Menu() gox.Component { return <div>Menu</div> }
`)
	pagePath := filepath.Join(root, "pages", "account", "index.gox")
	pageSource := `module page
import (
	menu "@menu.gox"
	gox "github.com/gox-lang/gox"
)
func Page() gox.Component { return <main><&menu.Me/*cursor*/ /></main> }
`
	writeWorkspaceGOX(t, pagePath, strings.Replace(pageSource, "<&menu.Me/*cursor*/ />", "<&menu.Menu />", 1))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	count := 0
	for _, item := range items {
		if item.Label == "&menu.Menu" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("menu completion count = %d, want 1: %#v", count, items)
	}
}

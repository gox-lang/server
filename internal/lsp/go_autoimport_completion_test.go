package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGoAutoImportCompletesStandardLibraryMembersWithExplicitAlias(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nfunc Build() { _ = strings.Sp/*cursor*/ }\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(source, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}
	packageSource := "module page\nfunc Build() { str/*cursor*/ }\n"
	packageItem := requireCompletionItem(t, lexicalItemsAtMarker(t, server,
		Document{URI: document.URI, Path: document.Path, Text: packageSource}, "/*cursor*/"), "strings")
	if len(packageItem.AdditionalTextEdits) != 1 || packageItem.AdditionalTextEdits[0].NewText != "\n\nimport strings \"strings\"\n" {
		t.Fatalf("standard library package auto-import = %#v", packageItem)
	}

	item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "Split")
	if len(item.AdditionalTextEdits) != 1 || item.AdditionalTextEdits[0].NewText != "\n\nimport strings \"strings\"\n" {
		t.Fatalf("standard library auto-import = %#v", item)
	}
}

func TestGoAutoImportCompletesProjectPackagesAndConvertsSingleImportToBlock(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := `module page
import gox "github.com/gox-lang/gox"
func Build() { _ = library.He/*cursor*/ }
`
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(source, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}

	item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "Hello")
	want := "import (\n\tgox \"github.com/gox-lang/gox\"\n\tlibrary \"example/library\"\n)"
	if len(item.AdditionalTextEdits) != 1 || item.AdditionalTextEdits[0].NewText != want {
		t.Fatalf("project package auto-import = %#v", item)
	}
}

func TestGoAutoImportUsesDeclaredPackageNameForLocalPackage(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), `package register
func Create() string { return "created" }
`)
	pagePath := filepath.Join(root, "pages", "index.gox")
	source := "module page\nfunc Build() { _ = register.Cr/*cursor*/ }\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(source, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}

	item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "Create")
	if len(item.AdditionalTextEdits) != 1 || item.AdditionalTextEdits[0].NewText != "\n\nimport register \"gox-editor-test/billing\"\n" {
		t.Fatalf("local package auto-import = %#v", item)
	}
}

func TestGoAutoImportCompletesUnqualifiedExportFromLocalPackage(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), `package register
func Register(name string) {}
`)
	pagePath := filepath.Join(root, "pages", "index.gox")
	source := "module page\nfunc Build() { Reg/*cursor*/ }\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(source, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}

	item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "Register")
	if !strings.HasPrefix(item.InsertText, "register.Register(") || len(item.AdditionalTextEdits) != 1 ||
		item.AdditionalTextEdits[0].NewText != "\n\nimport register \"gox-editor-test/billing\"\n" {
		t.Fatalf("unqualified local export auto-import = %#v", item)
	}
}

func TestGoAutoImportCompletesUnqualifiedStandardLibraryExport(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nfunc Build() { Spl/*cursor*/ }\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(source, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}

	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	var item CompletionItem
	for _, candidate := range items {
		if candidate.Label == "Split" && strings.HasPrefix(candidate.InsertText, "strings.Split(") {
			item = candidate
			break
		}
	}
	if !strings.HasPrefix(item.InsertText, "strings.Split(") || len(item.AdditionalTextEdits) != 1 ||
		item.AdditionalTextEdits[0].NewText != "\n\nimport strings \"strings\"\n" {
		t.Fatalf("unqualified strings.Split auto-import = %#v", items)
	}
	if item.LabelDetails == nil || item.LabelDetails.Description != "strings" {
		t.Fatalf("strings.Split completion provenance = %#v", item)
	}
}

func TestGoAutoImportCompletesUnqualifiedDependencyExport(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nfunc Build() { Com/*cursor*/ }\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(source, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}

	item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "Component")
	if item.InsertText != "gox.Component" || len(item.AdditionalTextEdits) != 1 ||
		item.AdditionalTextEdits[0].NewText != "\n\nimport gox \"github.com/gox-lang/gox\"\n" {
		t.Fatalf("unqualified dependency export auto-import = %#v", item)
	}
	if item.LabelDetails == nil || item.LabelDetails.Description != "github.com/gox-lang/gox" {
		t.Fatalf("gox.Component completion provenance = %#v", item)
	}
}

func TestGoAutoImportDoesNotDuplicateVisibleLocalSymbol(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nfunc Split() {}\nfunc Build() { Spl/*cursor*/ }\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(source, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}

	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	item := requireCompletionItem(t, items, "Split")
	if len(item.AdditionalTextEdits) != 0 || !strings.HasPrefix(item.InsertText, "Split(") {
		t.Fatalf("local Split completion = %#v", item)
	}
	for _, candidate := range items {
		if candidate.Label == "Split" && len(candidate.AdditionalTextEdits) > 0 {
			t.Fatalf("local Split was duplicated by auto-import: %#v", items)
		}
	}
}

func TestGoAutoImportRetainsDistinctPackagesWithSameExport(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nfunc Build() { Spl/*cursor*/ }\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(source, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}

	items := lexicalItemsAtMarker(t, server, document, "/*cursor*/")
	bytesIndex, stringsIndex := -1, -1
	for index, candidate := range items {
		if candidate.Label != "Split" {
			continue
		}
		switch {
		case strings.HasPrefix(candidate.InsertText, "bytes.Split("):
			bytesIndex = index
		case strings.HasPrefix(candidate.InsertText, "strings.Split("):
			stringsIndex = index
		}
	}
	if bytesIndex < 0 || stringsIndex < 0 || bytesIndex >= stringsIndex {
		t.Fatalf("Split auto-import alternatives = %#v", items)
	}
}

func TestGoAutoImportDiscoversGoxSDKAndAvoidsDuplicatesAndAliasConflicts(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nfunc Build() { var _ gox.Com/*cursor*/ }\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(source, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}

	item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "Component")
	if len(item.AdditionalTextEdits) != 1 || item.AdditionalTextEdits[0].NewText != "\n\nimport gox \"github.com/gox-lang/gox\"\n" {
		t.Fatalf("GOX SDK auto-import = %#v", item)
	}

	imported := `module page
import gox "github.com/gox-lang/gox"
func Build() { var _ gox.Com/*cursor*/ }
`
	if err := server.symbols.UpdateOpen(Document{URI: document.URI, Path: document.Path, Text: strings.ReplaceAll(imported, "/*cursor*/", "")}); err != nil {
		t.Fatal(err)
	}
	item = requireCompletionItem(t, lexicalItemsAtMarker(t, server, Document{URI: document.URI, Path: document.Path, Text: imported}, "/*cursor*/"), "Component")
	if len(item.AdditionalTextEdits) != 0 {
		t.Fatalf("existing SDK import produced edits: %#v", item)
	}

	conflict := "module page\nfunc Build() { gox := 1; _ = gox; gox/*cursor*/ }\n"
	if err := server.symbols.UpdateOpen(Document{URI: document.URI, Path: document.Path, Text: strings.ReplaceAll(conflict, "/*cursor*/", "")}); err != nil {
		t.Fatal(err)
	}
	items := lexicalItemsAtMarker(t, server, Document{URI: document.URI, Path: document.Path, Text: conflict}, "/*cursor*/")
	for _, candidate := range items {
		if candidate.Label == "gox" && len(candidate.AdditionalTextEdits) > 0 {
			t.Fatalf("conflicting alias received auto-import: %#v", candidate)
		}
	}
}

func writeGoAutoImportFixture(t *testing.T, root string) {
	t.Helper()
	sdk := filepath.Join(root, "sdk")
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), `module example

go 1.24

require github.com/gox-lang/gox v0.0.0
replace github.com/gox-lang/gox => ./sdk
`)
	writeGoPackageHoverFile(t, filepath.Join(sdk, "go.mod"), "module github.com/gox-lang/gox\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(sdk, "gox.go"), "package gox\ntype Component interface{}\ntype Context struct{}\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "library", "library.go"), "package library\nfunc Hello() string { return \"George\" }\n")
}

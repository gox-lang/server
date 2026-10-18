package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase15_3BBGlobalExportNameIndexIsShallow(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	index := NewGoPackageIndex()

	packages, err := index.ExportedPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, packageInfo := range packages {
		if packageInfo.Path != "strings" {
			continue
		}
		for _, exported := range packageInfo.Exports {
			if exported.Name != "Split" {
				continue
			}
			if exported.Kind != "func" {
				t.Fatalf("shallow strings.Split kind = %q", exported.Kind)
			}
			if exported.Detail != "" || exported.Type != "" || exported.Underlying != "" || exported.Value != "" ||
				len(exported.Results) != 0 || len(exported.Parameters) != 0 || len(exported.Members) != 0 || exported.Definition != (GoDefinition{}) {
				t.Fatalf("shallow strings.Split retained detailed metadata: %#v", exported)
			}
			return
		}
		t.Fatal("shallow strings index is missing Split")
	}
	t.Fatal("global export name index is missing strings")
}

func TestPhase15_3BBGlobalExportDetailsMaterializeOnlyForMatchingPackages(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nfunc Build() { Spl/*cursor*/ }\n"
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: strings.ReplaceAll(source, "/*cursor*/", "")}
	writeWorkspaceGOX(t, pagePath, document.Text)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}

	items := lexicalItemsAtMarker(t, server, Document{URI: document.URI, Path: document.Path, Text: source}, "/*cursor*/")
	var split CompletionItem
	for _, item := range items {
		if item.Label == "Split" && strings.HasPrefix(item.InsertText, "strings.Split(") {
			split = item
			break
		}
	}
	if split.Label == "" || split.Detail == "" || len(split.CandidateParameters) != 2 || len(split.CandidateResults) != 1 {
		t.Fatalf("lazy strings.Split completion lost detailed metadata: %#v", split)
	}

	server.goPackages.mu.RLock()
	_, stringsDetailed := server.goPackages.exportDetails[root+"\x00strings"]
	_, fmtDetailed := server.goPackages.exportDetails[root+"\x00fmt"]
	detailCount := 0
	for key := range server.goPackages.exportDetails {
		if strings.HasPrefix(key, root+"\x00") {
			detailCount++
		}
	}
	shallowCount := len(server.goPackages.exports[root])
	server.goPackages.mu.RUnlock()

	if !stringsDetailed {
		t.Fatal("matching strings package did not materialize detailed export metadata")
	}
	if fmtDetailed {
		t.Fatal("non-matching fmt package materialized detailed export metadata for prefix Spl")
	}
	if detailCount == 0 || shallowCount == 0 || detailCount >= shallowCount {
		t.Fatalf("lazy detail cache did not remain selective: details=%d shallowPackages=%d", detailCount, shallowCount)
	}
}

func TestPhase15_3BBExportedPackageDetailsAreCached(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	index := NewGoPackageIndex()
	if _, err := index.ExportedPackages(root); err != nil {
		t.Fatal(err)
	}

	first, err := index.ExportedPackageDetails(root, "strings")
	if err != nil {
		t.Fatal(err)
	}
	second, err := index.ExportedPackageDetails(root, "strings")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Exports) == 0 || len(second.Exports) != len(first.Exports) {
		t.Fatalf("cached strings exports = first:%d second:%d", len(first.Exports), len(second.Exports))
	}

	index.mu.RLock()
	_, ok := index.exportDetails[root+"\x00strings"]
	index.mu.RUnlock()
	if !ok {
		t.Fatal("strings detailed export projection was not cached")
	}
}

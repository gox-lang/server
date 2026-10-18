package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase15_3BLowercasePrefixSkipsGlobalExportIndex(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() {
    text := "hello"
    _ = strings.NewReplacer("a", "b").Replace(te/*cursor*/)
}
`
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: strings.ReplaceAll(source, "/*cursor*/", "")}
	writeWorkspaceGOX(t, pagePath, document.Text)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}

	items := lexicalItemsAtMarker(t, server, Document{URI: document.URI, Path: document.Path, Text: source}, "/*cursor*/")
	requireCompletionItem(t, items, "text")

	server.goPackages.mu.RLock()
	_, globalExportsBuilt := server.goPackages.exports[root]
	server.goPackages.mu.RUnlock()
	if globalExportsBuilt {
		t.Fatal("lowercase lexical prefix built the global Go export index")
	}
}

func TestPhase15_3BImportedPackageNameDoesNotRequireFullPackageLoad(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), `package register
func Create() string { return "created" }
`)
	pagePath := filepath.Join(root, "page.gox")
	source := `module page
import "example/billing"
func Build() { reg/*cursor*/ }
`
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: strings.ReplaceAll(source, "/*cursor*/", "")}
	writeWorkspaceGOX(t, pagePath, document.Text)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}

	offset := strings.Index(document.Text, "reg") + len("reg")
	state := server.newGoAutoImportCompletionState(document, offset)
	if candidate, ok := state.candidateByPath["example/billing"]; !ok || candidate.Name != "register" {
		t.Fatalf("discovered billing package = %#v, ok=%v", candidate, ok)
	}
	if !state.unavailable["register"] {
		t.Fatalf("implicit imported package alias was not reserved: %#v", state.unavailable)
	}

	server.goPackages.mu.RLock()
	_, fullyLoaded := server.goPackages.cache[root+"\x00"+"example/billing"]
	server.goPackages.mu.RUnlock()
	if fullyLoaded {
		t.Fatal("resolving an implicit import alias populated the full typed package cache")
	}
}

func TestPhase15_3BExportedPrefixStillBuildsGlobalExportIndex(t *testing.T) {
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
	foundStringsSplit := false
	for _, item := range items {
		if item.Label == "Split" && strings.HasPrefix(item.InsertText, "strings.Split(") {
			foundStringsSplit = true
			break
		}
	}
	if !foundStringsSplit {
		t.Fatalf("exported prefix lost strings.Split auto-import: %#v", items)
	}

	server.goPackages.mu.RLock()
	_, globalExportsBuilt := server.goPackages.exports[root]
	server.goPackages.mu.RUnlock()
	if !globalExportsBuilt {
		t.Fatal("exported prefix did not build the global Go export index")
	}
}

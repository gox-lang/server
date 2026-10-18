package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefinitionNavigatesToImportedGoPackageAndExports(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	apiPath := filepath.Join(root, "library", "api.go")
	apiSource := `// Ω keeps byte and UTF-16 columns distinct.
package api

type User struct{ Name string }
const Limit = 10
func SplitName(value string) (string, string) { return value, value }
func privateFunction() {}
`
	writeGoPackageHoverFile(t, apiPath, apiSource)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import library "example/library"
func Build() {
	var _ library.User
	_ = library.Limit
	_, _ = library.SplitName("George")
	_ = library.privateFunction
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	assertGoDefinition(t, server, document, strings.Index(pageSource, "library.User")+1, apiPath, apiSource, strings.Index(apiSource, "api\n"))
	assertGoDefinition(t, server, document, strings.Index(pageSource, "library.User")+len("library."), apiPath, apiSource, strings.Index(apiSource, "User struct"))
	assertGoDefinition(t, server, document, strings.Index(pageSource, "library.Limit")+len("library."), apiPath, apiSource, strings.Index(apiSource, "Limit ="))
	assertGoDefinition(t, server, document, strings.Index(pageSource, "library.SplitName")+len("library."), apiPath, apiSource, strings.Index(apiSource, "SplitName("))

	private := definitionLocationsAtOffset(t, server, document, strings.Index(pageSource, "privateFunction")+1)
	if len(private) != 0 {
		t.Fatalf("private Go member locations = %#v", private)
	}
}

func assertGoDefinition(t *testing.T, server *Server, source Document, useOffset int, targetPath, targetText string, targetOffset int) {
	t.Helper()
	locations := definitionLocationsAtOffset(t, server, source, useOffset)
	target := Document{URI: FileURI(targetPath), Path: targetPath, Text: targetText}
	want, err := target.Position(targetOffset)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 || locations[0].URI != target.URI || locations[0].Range.Start != want {
		t.Fatalf("Go definition at %d = %#v, want %s:%#v", useOffset, locations, target.URI, want)
	}
}

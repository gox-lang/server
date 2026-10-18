package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefinitionUsesQualifiedGOXImportInsteadOfGlobalName(t *testing.T) {
	root := t.TempDir()
	layoutPath := filepath.Join(root, "layout.gox")
	otherPath := filepath.Join(root, "other.gox")
	pagePath := filepath.Join(root, "pages", "index.gox")
	writeWorkspaceGOX(t, layoutPath, "module Layout\nfunc Header() gox.Component { return <header>George</header> }\n")
	writeWorkspaceGOX(t, otherPath, "module Other\nfunc Header() gox.Component { return <header>Other</header> }\n")
	pageSource := "module page\nimport Layout \"../layout.gox\"\nfunc Render() gox.Component { return <&Layout.Header/> }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	position, err := document.Position(strings.Index(pageSource, "Header") + 2)
	if err != nil {
		t.Fatal(err)
	}
	locations := server.definitionLocations(document, position)
	if len(locations) != 1 || locations[0].URI != FileURI(layoutPath) {
		t.Fatalf("qualified definition locations = %#v", locations)
	}
}

func TestDefinitionKeepsClientReferencesFileScoped(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "pages", "first.gox")
	secondPath := filepath.Join(root, "pages", "second.gox")
	firstSource := "module first\nclient Menu react { src: \"menu.jsx\" }\nfunc Render() gox.Component { return <@Menu/> }\n"
	writeWorkspaceGOX(t, firstPath, firstSource)
	writeWorkspaceGOX(t, secondPath, "module second\nclient Menu react { src: \"other.jsx\" }\n")

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(firstPath))
	if !ok {
		t.Fatal("first document was not indexed")
	}
	position, err := document.Position(strings.LastIndex(firstSource, "Menu") + 2)
	if err != nil {
		t.Fatal(err)
	}
	locations := server.definitionLocations(document, position)
	if len(locations) != 1 || locations[0].URI != FileURI(firstPath) {
		t.Fatalf("client definition locations = %#v", locations)
	}
}

func TestDefinitionPrefersLocalComponentVariable(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "pages", "index.gox")
	otherPath := filepath.Join(root, "other.gox")
	pageSource := "module page\nfunc Render() gox.Component {\nCard := func() gox.Component { return <span>George</span> }\nreturn <&Card/>\n}\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	writeWorkspaceGOX(t, otherPath, "module other\nfunc Card() gox.Component { return <span>Other</span> }\n")

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	position, err := document.Position(strings.LastIndex(pageSource, "Card") + 2)
	if err != nil {
		t.Fatal(err)
	}
	locations := server.definitionLocations(document, position)
	declarationOffset := strings.Index(pageSource, "Card")
	wantPosition, err := document.Position(declarationOffset)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 || locations[0].URI != FileURI(pagePath) || locations[0].Range.Start != wantPosition {
		t.Fatalf("local component definition locations = %#v", locations)
	}
}

func writeWorkspaceGOX(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

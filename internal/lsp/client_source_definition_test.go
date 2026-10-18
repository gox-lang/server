package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientSourceDefinitionResolvesFrameworkDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/clienttest\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	frameworks := []struct {
		name      string
		extension string
	}{
		{name: "react", extension: ".jsx"},
		{name: "preact", extension: ".jsx"},
		{name: "solid", extension: ".jsx"},
		{name: "vue", extension: ".vue"},
		{name: "svelte", extension: ".svelte"},
	}
	server := NewServer()
	for _, framework := range frameworks {
		t.Run(framework.name, func(t *testing.T) {
			target := filepath.Join(root, "client", framework.name, "menu", "menu"+framework.extension)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("export default function Menu() {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			source := "module page\n\nclient Menu " + framework.name + " {\n\tsrc: \"menu/menu" + framework.extension + "\"\n}\n"
			document := Document{URI: FileURI(filepath.Join(root, "pages", framework.name+".gox")), Path: filepath.Join(root, "pages", framework.name+".gox"), Text: source}
			position, err := document.Position(strings.Index(source, "menu/menu") + 2)
			if err != nil {
				t.Fatal(err)
			}
			links, ok := server.definitionResult(document, position).([]LocationLink)
			if !ok || len(links) != 1 || links[0].TargetURI != FileURI(target) {
				t.Fatalf("client source links = %#v, want %s", links, FileURI(target))
			}
			start, _ := document.Offset(links[0].OriginSelectionRange.Start)
			end, _ := document.Offset(links[0].OriginSelectionRange.End)
			if source[start:end] != "menu/menu"+framework.extension {
				t.Fatalf("origin selection = %q", source[start:end])
			}
		})
	}
}

func TestClientSourceDefinitionNormalizesPrefixesAndRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/clienttest\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "client", "react", "menu", "menu.jsx")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("export default function Menu() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServer()
	for _, entry := range []string{"menu/menu.jsx", "react/menu/menu.jsx", "client/react/menu/menu.jsx"} {
		source := "module page\nclient Menu react { src: \"" + entry + "\" }\n"
		document := Document{URI: FileURI(filepath.Join(root, "pages", "page.gox")), Path: filepath.Join(root, "pages", "page.gox"), Text: source}
		position, _ := document.Position(strings.Index(source, entry) + 1)
		links, ok := server.definitionResult(document, position).([]LocationLink)
		if !ok || len(links) != 1 || links[0].TargetURI != FileURI(target) {
			t.Fatalf("entry %q links = %#v", entry, links)
		}
	}
	escape := "module page\nclient Menu react { src: \"../outside.jsx\" }\n"
	document := Document{URI: FileURI(filepath.Join(root, "pages", "escape.gox")), Path: filepath.Join(root, "pages", "escape.gox"), Text: escape}
	position, _ := document.Position(strings.Index(escape, "../outside") + 1)
	if links, ok := server.definitionResult(document, position).([]LocationLink); ok && len(links) != 0 {
		t.Fatalf("escaping client source returned %#v", links)
	}
}

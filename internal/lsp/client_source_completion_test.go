package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClientSourceCompletionListsOnlyFrameworkFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/clienttest\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeClientCompletionFile(t, root, "react", "menu/menu.jsx")
	writeClientCompletionFile(t, root, "react", "menu/menu.tsx")
	writeClientCompletionFile(t, root, "react", "menu/ignored.js")
	writeClientCompletionFile(t, root, "solid", "menu/solid.jsx")
	source := "module page\nclient Menu react {\n\tsrc: \"menu/\"\n}\n"
	document := Document{URI: FileURI(filepath.Join(root, "pages", "index.gox")), Path: filepath.Join(root, "pages", "index.gox"), Text: source}
	position, _ := document.Position(len(source) - len("\"\n}\n"))
	items := NewServer().completions(document, position).Items
	if len(items) != 2 || items[0].Label != "menu/menu.jsx" || items[1].Label != "menu/menu.tsx" {
		t.Fatalf("client source completions = %#v", items)
	}
	for _, item := range items {
		if item.TextEdit == nil || item.TextEdit.NewText != item.Label {
			t.Fatalf("client source completion edit = %#v", item)
		}
	}
}

func TestClientSourceCompletionWorksInIncompleteLiteral(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/clienttest\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeClientCompletionFile(t, root, "vue", "menu/menu.vue")
	source := "module page\nclient Menu vue {\n\tsrc: \"men"
	document := Document{URI: FileURI(filepath.Join(root, "pages", "index.gox")), Path: filepath.Join(root, "pages", "index.gox"), Text: source}
	position, _ := document.Position(len(source))
	items := NewServer().completions(document, position).Items
	if len(items) != 1 || items[0].Label != "menu/menu.vue" {
		t.Fatalf("incomplete client source completions = %#v", items)
	}
}

func writeClientCompletionFile(t *testing.T, root, framework, relative string) {
	t.Helper()
	path := filepath.Join(root, "client", framework, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("export default {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

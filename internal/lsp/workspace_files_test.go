package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndexWorkspaceRootsLoadsGOXFilesAndSkipsIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	componentPath := filepath.Join(root, "components", "card.gox")
	hiddenPath := filepath.Join(root, ".toolchain", "hidden.gox")
	if err := os.MkdirAll(filepath.Dir(componentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(hiddenPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(componentPath, []byte("module card\nfunc Card() string { return \"George\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hiddenPath, []byte("module hidden\nfunc Hidden() string { return \"George\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	definitions := server.SymbolDefinitions("Card")
	if len(definitions) != 1 || definitions[0].URI != FileURI(componentPath) {
		t.Fatalf("Card definitions = %#v", definitions)
	}
	if definitions := server.SymbolDefinitions("Hidden"); len(definitions) != 0 {
		t.Fatalf("ignored definitions = %#v", definitions)
	}
}

func TestWorkspaceRootsPreferWorkspaceFolders(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	params := InitializeParams{
		RootURI: FileURI(first),
		WorkspaceFolders: []WorkspaceFolder{
			{URI: FileURI(second), Name: "second"},
		},
	}
	roots := workspaceRoots(params)
	if len(roots) != 1 || roots[0] != filepath.Clean(second) {
		t.Fatalf("workspace roots = %#v", roots)
	}
}

func TestFileURIRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "George Márquez", "index.gox")
	uri := FileURI(path)
	decoded, err := FilePath(uri)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != filepath.Clean(path) {
		t.Fatalf("FilePath(FileURI(%q)) = %q", path, decoded)
	}
}

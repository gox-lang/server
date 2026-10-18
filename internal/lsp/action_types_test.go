package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLSPActionTypesInitialIndex(t *testing.T) {
	root := t.TempDir()
	writeLSPActionProjectFile(t, root, "pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Login(email string) gox.Action { return true }
`)
	NewServer().indexWorkspaceRoots([]string{root})
	assertLSPActionTypesContains(t, root, "Login(email: string): Promise<[boolean | null, GoxActionError | null]>")
}

func TestLSPActionTypesUseUnsavedOpenDocument(t *testing.T) {
	root := t.TempDir()
	path := writeLSPActionProjectFile(t, root, "pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Login(email string) gox.Action { return true }
`)
	changed := `module index
import "github.com/gox-lang/gox"
func Login(email string, password string) gox.Action { return "ok" }
`
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + FileURI(path) + `","languageId":"gox","version":1,"text":` + strconv.Quote(changed) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	if err := NewServer().Serve(strings.NewReader(input), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data := readLSPActionTypes(t, root)
	if !strings.Contains(data, "Login(email: string, password: string): Promise<[string | null, GoxActionError | null]>") || strings.Contains(data, "Promise<[boolean | null") {
		t.Fatalf("actions.d.ts did not use unsaved document:\n%s", data)
	}
}

func TestLSPActionTypesDeleteAndRename(t *testing.T) {
	root := t.TempDir()
	oldPath := writeLSPActionProjectFile(t, root, "pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Old() gox.Action { return true }
`)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	assertLSPActionTypesContains(t, root, "Old():")
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	server.handleWorkspaceFileChange(FileURI(oldPath), fileDeleted)
	if strings.Contains(readLSPActionTypes(t, root), "Old():") {
		t.Fatal("deleted Action remained in generated typings")
	}

	newPath := writeLSPActionProjectFile(t, root, "pages/login.gox", `module login
import "github.com/gox-lang/gox"
func Login() gox.Action { return "ok" }
`)
	server.handleWorkspaceFileChange(FileURI(newPath), fileCreated)
	assertLSPActionTypesContains(t, root, "Login(): Promise<[string | null, GoxActionError | null]>")
}

func TestLSPActionTypesInitialIndexPrunesStaleCache(t *testing.T) {
	root := t.TempDir()
	path := writeLSPActionProjectFile(t, root, "pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Stale() gox.Action { return true }
`)
	NewServer().indexWorkspaceRoots([]string{root})
	assertLSPActionTypesContains(t, root, "Stale():")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	NewServer().indexWorkspaceRoots([]string{root})
	if data := readLSPActionTypes(t, root); strings.Contains(data, "Stale():") {
		t.Fatalf("fresh workspace index retained stale Action:\n%s", data)
	}
}

func writeLSPActionProjectFile(t *testing.T, root, relative, contents string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readLSPActionTypes(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertLSPActionTypesContains(t *testing.T, root, want string) {
	t.Helper()
	data := readLSPActionTypes(t, root)
	if !strings.Contains(data, want) {
		t.Fatalf("actions.d.ts missing %q:\n%s", want, data)
	}
}

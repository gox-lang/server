package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPGeneratedTypesSyncOneDocumentAcrossActionsAndWebSocket(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "pages", "index.gox")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	source := `module index
import "github.com/gox-lang/gox"
func Login() gox.Action { return true }
func Notify() { gox.Sockets.Emit("notice", "ready") }
`
	server := NewServer()
	server.syncGeneratedTypes(Document{URI: FileURI(path), Path: path, LanguageID: "gox", Text: source})

	actions := readLSPActionTypes(t, root)
	if !strings.Contains(actions, "Login(): Promise<[boolean | null, GoxActionError | null]>") {
		t.Fatalf("actions.d.ts did not receive shared document sync:\n%s", actions)
	}
	events := readLSPWebSocketEventTypes(t, root)
	if !strings.Contains(events, `"notice": WebSocketEventContract<readonly [arg1: string], never>`) {
		t.Fatalf("websocket-events.d.ts did not receive shared document sync:\n%s", events)
	}
}

func TestLSPGeneratedTypesInvalidDocumentRemovesBothCaches(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "pages", "index.gox")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	valid := `module index
import "github.com/gox-lang/gox"
func Login() gox.Action { return true }
func Notify() { gox.Sockets.Emit("notice", "ready") }
`
	server := NewServer()
	document := Document{URI: FileURI(path), Path: path, LanguageID: "gox", Text: valid}
	server.syncGeneratedTypes(document)
	assertLSPActionTypesContains(t, root, "Login():")
	assertLSPWebSocketEventTypesContains(t, root, `"notice":`)

	document.Text = "module index\nfunc Broken("
	server.syncGeneratedTypes(document)
	if data := readLSPActionTypes(t, root); strings.Contains(data, "Login():") {
		t.Fatalf("invalid document retained Action contract:\n%s", data)
	}
	if data := readLSPWebSocketEventTypes(t, root); strings.Contains(data, `"notice":`) {
		t.Fatalf("invalid document retained WebSocket contract:\n%s", data)
	}
}

func TestLSPGeneratedTypesRemovalClearsBothCaches(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "pages", "index.gox")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	source := `module index
import "github.com/gox-lang/gox"
func Login() gox.Action { return true }
func Notify() { gox.Sockets.Emit("notice", "ready") }
`
	server := NewServer()
	server.syncGeneratedTypes(Document{URI: FileURI(path), Path: path, LanguageID: "gox", Text: source})
	server.removeGeneratedTypes(path)

	if data := readLSPActionTypes(t, root); strings.Contains(data, "Login():") {
		t.Fatalf("removed source retained Action contract:\n%s", data)
	}
	if data := readLSPWebSocketEventTypes(t, root); strings.Contains(data, `"notice":`) {
		t.Fatalf("removed source retained WebSocket contract:\n%s", data)
	}
}

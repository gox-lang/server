package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLSPWebSocketEventTypesInitialIndex(t *testing.T) {
	root := t.TempDir()
	writeLSPWebSocketProjectFile(t, root, "websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("login", func(user string) { })
}
`)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	assertLSPWebSocketEventTypesContains(t, root, `"login": WebSocketEventContract<readonly [user: string], never>`)
}

func TestLSPWebSocketEventTypesUseUnsavedOpenDocument(t *testing.T) {
	root := t.TempDir()
	path := writeLSPWebSocketProjectFile(t, root, "websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("old", func(value string) { })
}
`)
	uri := FileURI(path)
	changed := `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("new", func(value int) { })
}
`
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + strconv.Quote(changed) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	if err := NewServer().Serve(strings.NewReader(input), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data := readLSPWebSocketEventTypes(t, root)
	if !strings.Contains(data, `"new": WebSocketEventContract<readonly [value: number], never>`) || strings.Contains(data, `"old":`) {
		t.Fatalf("websocket-events.d.ts =\n%s", data)
	}
}

func TestLSPWebSocketEventTypesDeleteAndRename(t *testing.T) {
	root := t.TempDir()
	oldPath := writeLSPWebSocketProjectFile(t, root, "websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket { socket.On("old", func() { }) }
`)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	server.handleWorkspaceFileChange(FileURI(oldPath), fileDeleted)
	if strings.Contains(readLSPWebSocketEventTypes(t, root), `"old":`) {
		t.Fatal("deleted WebSocket event remained in generated typings")
	}
	newPath := writeLSPWebSocketProjectFile(t, root, "websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket { socket.On("renamed", func() { }) }
`)
	server.handleWorkspaceFileChange(FileURI(newPath), fileCreated)
	assertLSPWebSocketEventTypesContains(t, root, `"renamed": WebSocketEventContract<readonly [], never>`)
}

func TestLSPWebSocketEventTypesInitialIndexPrunesStaleCache(t *testing.T) {
	root := t.TempDir()
	path := writeLSPWebSocketProjectFile(t, root, "websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket { socket.On("stale", func() { }) }
`)
	NewServer().indexWorkspaceRoots([]string{root})
	assertLSPWebSocketEventTypesContains(t, root, `"stale":`)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	NewServer().indexWorkspaceRoots([]string{root})
	if data := readLSPWebSocketEventTypes(t, root); strings.Contains(data, `"stale":`) {
		t.Fatalf("fresh workspace index retained stale WebSocket event:\n%s", data)
	}
}

func TestLSPWebSocketEventTypesUseProjectRelativeStructuralContext(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "websocket", "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLSPWebSocketProjectFile(t, root, "chat.gox", `module chat
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel {
	channel.On("message", func(socket *gox.Socket, value string) { })
}
`)

	NewServer().indexWorkspaceRoots([]string{root})
	data := readLSPWebSocketEventTypes(t, root)
	if strings.Contains(data, `"message":`) || strings.Contains(data, `"Project/Chat"`) {
		t.Fatalf("absolute parent directory affected WebSocket structural authority:\n%s", data)
	}
}

func TestLSPWebSocketEventTypesChannelRenameReplacesStructuralIdentity(t *testing.T) {
	root := t.TempDir()
	oldPath := writeLSPWebSocketProjectFile(t, root, "websocket/chat.gox", `module chat
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel { channel.On("message", func(socket *gox.Socket) { }) }
`)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	assertLSPWebSocketEventTypesContains(t, root, `Chat: {`)

	newPath := filepath.Join(root, "websocket", "room.gox")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte(`module room
import "github.com/gox-lang/gox"
func Room(channel *gox.Channel) gox.Channel { channel.On("message", func(socket *gox.Socket) { }) }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	server.handleWorkspaceFileChange(FileURI(oldPath), fileDeleted)
	server.handleWorkspaceFileChange(FileURI(newPath), fileCreated)
	data := readLSPWebSocketEventTypes(t, root)
	if strings.Contains(data, `Chat: {`) || !strings.Contains(data, `Room: {`) {
		t.Fatalf("renamed Channel identity was not replaced:\n%s", data)
	}
}

func writeLSPWebSocketProjectFile(t *testing.T, root, relative, contents string) string {
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

func readLSPWebSocketEventTypes(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".build", "websocket-events.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertLSPWebSocketEventTypesContains(t *testing.T, root, want string) {
	t.Helper()
	data := readLSPWebSocketEventTypes(t, root)
	if !strings.Contains(data, want) {
		t.Fatalf("websocket-events.d.ts missing %q:\n%s", want, data)
	}
}

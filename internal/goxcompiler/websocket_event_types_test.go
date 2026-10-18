package gox

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestUpdateWebSocketEventTypesWritesSocketDirectionsAndReplies(t *testing.T) {
	root := t.TempDir()
	writeWSProjectRoot(t, root)
	path := filepath.Join(root, "websocket", "index.gox")
	source := `module websocket
import "github.com/gox-lang/gox"
type Profile struct { Name string ` + "`json:\"name\"`" + ` }
type Theme struct { Name string ` + "`json:\"name\"`" + ` }
func Index(socket *gox.Socket) gox.WebSocket {
 socket.On("profile", func(id string) gox.EventReply { return Profile{Name:id} })
 socket.Emit("notice", "ready")
 var theme Theme
 socket.Emit("theme", 7).Await(&theme)
}`
	writeWSFixture(t, path, source)
	graph, err := LoadGraph(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateWebSocketEventTypes(graph); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "websocket-events.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`declare module "@gox/websocket-events"`,
		`export interface SocketClientEmitEvents`,
		`"profile": WebSocketEventContract<readonly [id: string], { name: string }>`,
		`export interface SocketClientOnEvents`,
		`"notice": WebSocketEventContract<readonly [arg1: string], never>`,
		`"theme": WebSocketEventContract<readonly [arg1: number], { name: string }>`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".build", "cache", "websocket-event-types.json")); err != nil {
		t.Fatalf("missing websocket event type cache: %v", err)
	}
}

func TestUpdateWebSocketEventTypesSeparatesChannelsAndSameEventNames(t *testing.T) {
	root := t.TempDir()
	writeWSProjectRoot(t, root)
	socketPath := filepath.Join(root, "websocket", "index.gox")
	chatPath := filepath.Join(root, "websocket", "chat.gox")
	financePath := filepath.Join(root, "websocket", "finance.gox")
	writeWSFixture(t, socketPath, `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket { socket.On("message", func(value bool) {}) }`)
	writeWSFixture(t, chatPath, `module chat
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel { channel.On("message", func(socket *gox.Socket, text string) {}); channel.Emit("message", 7) }`)
	writeWSFixture(t, financePath, `module finance
import "github.com/gox-lang/gox"
func Finance(channel *gox.Channel) gox.Channel { channel.On("message", func(socket *gox.Socket, amount float64) {}); channel.Emit("message", true) }`)
	for _, path := range []string{socketPath, chatPath, financePath} {
		graph, err := LoadGraph(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := updateWebSocketEventTypes(graph); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "websocket-events.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`"message": WebSocketEventContract<readonly [value: boolean], never>`,
		`Chat: {`, `"message": WebSocketEventContract<readonly [text: string], never>`,
		`Finance: {`, `"message": WebSocketEventContract<readonly [amount: number], never>`,
		`"message": WebSocketEventContract<readonly [arg1: number], never>`,
		`"message": WebSocketEventContract<readonly [arg1: boolean], never>`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestUpdateWebSocketEventTypesDropsDeletedSources(t *testing.T) {
	root := t.TempDir()
	writeWSProjectRoot(t, root)
	oldPath := filepath.Join(root, "pages", "old.gox")
	currentPath := filepath.Join(root, "websocket", "index.gox")
	writeWSFixture(t, oldPath, `module old
import "github.com/gox-lang/gox"
func Notify() gox.Action { gox.Sockets.Emit("old-event", "value"); return true }`)
	graph, err := LoadGraph(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateWebSocketEventTypes(graph); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	writeWSFixture(t, currentPath, `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket { socket.On("current-event", func(value string) {}) }`)
	graph, err = LoadGraph(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateWebSocketEventTypes(graph); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "websocket-events.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "old-event") {
		t.Fatalf("deleted source remained:\n%s", data)
	}
	if !strings.Contains(string(data), "current-event") {
		t.Fatalf("current event missing:\n%s", data)
	}
}

func TestUpdateWebSocketEventTypesIsDeterministicAndConcurrent(t *testing.T) {
	root := t.TempDir()
	writeWSProjectRoot(t, root)
	paths := []string{filepath.Join(root, "websocket", "index.gox"), filepath.Join(root, "pages", "a.gox")}
	sources := []string{
		`module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket { socket.On("zeta", func(value string) {}); socket.On("alpha", func(value int) {}) }`,
		`module a
import "github.com/gox-lang/gox"
func Notify() gox.Action { gox.Sockets.Emit("beta", true); return true }`,
	}
	graphs := make([]*ModuleGraph, 0, len(paths))
	for i, path := range paths {
		writeWSFixture(t, path, sources[i])
		graph, err := LoadGraph(path)
		if err != nil {
			t.Fatal(err)
		}
		graphs = append(graphs, graph)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(graphs))
	for _, graph := range graphs {
		wg.Add(1)
		go func(g *ModuleGraph) { defer wg.Done(); errs <- updateWebSocketEventTypes(g) }(graph)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, ".build", "websocket-events.d.ts")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, graph := range graphs {
		if err := updateWebSocketEventTypes(graph); err != nil {
			t.Fatal(err)
		}
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("output changed without semantic changes:\nFIRST:\n%s\nSECOND:\n%s", first, second)
	}
	text := string(second)
	if strings.Index(text, `"alpha"`) > strings.Index(text, `"zeta"`) {
		t.Fatalf("events are not sorted:\n%s", text)
	}
}

func writeWSFixture(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeWSProjectRoot(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

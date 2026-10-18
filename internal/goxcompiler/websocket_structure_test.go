package gox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebSocketEntrypointRequiresGlobalStructure(t *testing.T) {
	source := `module index
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {}`

	for _, path := range []string{
		"pages/index.gox",
		"websocket/chat.gox",
		"websocket/chat/index.gox",
		"websocket/private.helper.gox",
		"other.gox",
	} {
		t.Run(path, func(t *testing.T) {
			_, err := ParseModule(path, source)
			if err == nil || !strings.Contains(err.Error(), "outside websocket/index.gox") {
				t.Fatalf("expected WebSocket location error for %s, got %v", path, err)
			}
		})
	}
}

func TestGlobalWebSocketRequiresWebsocketModule(t *testing.T) {
	_, err := ParseModule("websocket/index.gox", `module socket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {}`)
	if err == nil || !strings.Contains(err.Error(), "must declare module websocket") {
		t.Fatalf("expected WebSocket module error, got %v", err)
	}
}

func TestGlobalWebSocketRequiresEntrypoint(t *testing.T) {
	_, err := ParseModule("websocket/index.gox", `module websocket
func helper() {}`)
	if err == nil || !strings.Contains(err.Error(), "does not declare the global WebSocket entrypoint") {
		t.Fatalf("expected missing WebSocket entrypoint error, got %v", err)
	}
}

func TestLoadGraphUsesProjectRelativeWebSocketStructure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wsDir := filepath.Join(root, "websocket")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(wsDir, "index.gox")
	if err := os.WriteFile(entry, []byte(`module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	graph, err := LoadGraph(entry)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Entry.Structure.RelativePath != "websocket/index.gox" {
		t.Fatalf("structure = %#v", graph.Entry.Structure)
	}
}

func TestLoadGraphRejectsWebSocketEntrypointOutsideGlobalFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pagesDir := filepath.Join(root, "pages")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(pagesDir, "index.gox")
	if err := os.WriteFile(entry, []byte(`module index
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadGraph(entry)
	if err == nil || !strings.Contains(err.Error(), "outside websocket/index.gox") {
		t.Fatalf("expected project-relative WebSocket location error, got %v", err)
	}
}

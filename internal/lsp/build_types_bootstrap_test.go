package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPInitialIndexBootstrapsBuildTypes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	NewServer().indexWorkspaceRoots([]string{root})

	for _, relative := range []string{
		".build/actions.d.ts",
		".build/websocket.d.ts",
		".build/websocket-events.d.ts",
		".build/cache/actions-types.json",
		".build/cache/websocket-event-types.json",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("bootstrap did not create %s: %v", relative, err)
		}
		if info.IsDir() {
			t.Fatalf("bootstrap path %s is a directory", relative)
		}
	}

	websocket, err := os.ReadFile(filepath.Join(root, ".build", "websocket.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(websocket), `declare module "@gox/websocket"`) {
		t.Fatalf("websocket.d.ts does not contain the static GOX WebSocket API:\n%s", websocket)
	}

	actions, err := os.ReadFile(filepath.Join(root, ".build", "actions.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(actions), `declare module "@gox/actions"`) || !strings.Contains(string(actions), "export interface ServerActions") {
		t.Fatalf("actions.d.ts does not contain the empty generated Actions contract:\n%s", actions)
	}

	events, err := os.ReadFile(filepath.Join(root, ".build", "websocket-events.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), `declare module "@gox/websocket-events"`) || !strings.Contains(string(events), "export interface SocketClientEmitEvents") {
		t.Fatalf("websocket-events.d.ts does not contain the empty generated WebSocket event contract:\n%s", events)
	}
}

func TestLSPInitialIndexBootstrapsEachGOXProject(t *testing.T) {
	workspace := t.TempDir()
	for _, name := range []string{"one", "two"} {
		root := filepath.Join(workspace, name)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/"+name+"\n\ngo 1.24\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	NewServer().indexWorkspaceRoots([]string{workspace})

	for _, name := range []string{"one", "two"} {
		root := filepath.Join(workspace, name)
		for _, relative := range []string{"actions.d.ts", "websocket.d.ts", "websocket-events.d.ts"} {
			if _, err := os.Stat(filepath.Join(root, ".build", relative)); err != nil {
				t.Fatalf("project %s missing %s after workspace bootstrap: %v", name, relative, err)
			}
		}
	}
}

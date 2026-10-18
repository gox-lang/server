package serverruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/project"
	"github.com/gox-lang/gox/server"
)

func TestChannelDiscoveryAfterStartup(t *testing.T) {
	for _, development := range []bool{false, true} {
		name := "production"
		if development {
			name = "development"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			runtime := &Runtime{project: &project.Project{RootDir: root}}
			runtime.runtimeState.Store(&RuntimeState{Config: server.Config{Dev: server.DevConfig{Enabled: development}}})
			if err := runtime.indexChannelDefinitions(); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.findChannelDefinition("Chat"); err == nil {
				t.Fatal("missing definition accepted")
			}
			directory := filepath.Join(root, "websocket", "chat")
			if err := os.MkdirAll(directory, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "chat.gox"), []byte("module chat\n"), 0644); err != nil {
				t.Fatal(err)
			}
			definition, err := runtime.findChannelDefinition("Chat")
			if !development {
				if err == nil {
					t.Fatal("production unexpectedly rescanned definitions")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if definition.entry.File != "websocket/chat/chat.gox" {
				t.Fatalf("unexpected definition: %#v", definition.entry)
			}
			definition.version = "live-generation"
			if err := os.WriteFile(filepath.Join(directory, "support.gox"), []byte("module support\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.findChannelDefinition("Chat/Support"); err != nil {
				t.Fatal(err)
			}
			existing, err := runtime.findChannelDefinition("Chat")
			if err != nil {
				t.Fatal(err)
			}
			if existing != definition || existing.version != "live-generation" {
				t.Fatal("rescan replaced a live definition")
			}
		})
	}
}

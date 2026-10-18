package scaffold_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/project"
	"github.com/gox-lang/server/internal/scaffold"
)

func TestMinimalProjectAndOptionalWebSocket(t *testing.T) {
	opts := scaffold.Options{ProjectName: "demo", TargetDir: t.TempDir(), GoVersion: "1.26", GoxModule: "github.com/gox-lang/gox", GoxModuleVersion: "v0.1.0"}
	if err := scaffold.CreateProject(cli.Context{}, opts); err != nil {
		t.Fatal(err)
	}
	root := opts.ProjectPath()
	if _, err := project.Detect(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"client", "components", "websocket", "tests/results", "tests/snapshots", ".build/client"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s: %v", name, err)
		}
	}
	if err := scaffold.EnsureWebSocket(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "websocket", "index.gox")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != scaffold.WebSocketIndexSource {
		t.Fatal("unexpected entrypoint")
	}
	custom := []byte("user code")
	if err := os.WriteFile(path, custom, 0644); err != nil {
		t.Fatal(err)
	}
	if err := scaffold.EnsureWebSocket(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(custom) {
		t.Fatal("user code overwritten")
	}
}

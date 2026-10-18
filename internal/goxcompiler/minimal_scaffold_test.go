package gox

import (
	"github.com/gox-lang/server/internal/scaffold"
	"os"
	"path/filepath"
	"testing"
)

func TestMinimalScaffoldConfigurationCompiles(t *testing.T) {
	root := t.TempDir()
	if err := scaffold.WriteDirs(root, scaffold.ProjectDirs()); err != nil {
		t.Fatal(err)
	}
	if err := scaffold.WriteFiles(root, scaffold.ProjectFiles(scaffold.Options{ProjectName: "demo"})); err != nil {
		t.Fatal(err)
	}
	phase1CopyModuleFiles(t, root)
	output := filepath.Join(root, "generated.go")
	if _, err := Compile(filepath.Join(root, "app", "config.gox"), output); err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	phase1GoTestGeneratedPackage(t, root, string(code))
}

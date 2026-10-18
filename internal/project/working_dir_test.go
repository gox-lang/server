package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUseAsWorkingDirChangesToProjectRoot(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	root := t.TempDir()
	p := &Project{RootDir: root}
	if err := p.UseAsWorkingDir(); err != nil {
		t.Fatalf("UseAsWorkingDir failed: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd after UseAsWorkingDir failed: %v", err)
	}
	if filepath.Clean(wd) != filepath.Clean(root) {
		t.Fatalf("working dir = %q, want %q", wd, root)
	}
}

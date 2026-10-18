package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupConfigBuildArtifactsKeepsCurrentBuild(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, ".build", "config")
	oldDir := filepath.Join(configRoot, "old")
	currentDir := filepath.Join(configRoot, "current")
	for _, dir := range []string{oldDir, currentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.so"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupConfigBuildArtifacts(root, currentDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(currentDir); err != nil {
		t.Fatalf("current build missing: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old build still exists, err=%v", err)
	}
}

func TestCleanupConfigBuildArtifactsRejectsOutsideConfig(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cleanupConfigBuildArtifacts(root, outside); err == nil {
		t.Fatal("expected safety error")
	}
}

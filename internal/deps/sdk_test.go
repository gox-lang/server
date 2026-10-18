package deps

import (
	"github.com/gox-lang/server/internal/buildinfo"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSDKMatchingSkipsGo(t *testing.T) {
	root := t.TempDir()
	cfg := NewGoxJSON()
	cfg.Dependencies[buildinfo.GoxModule] = buildinfo.GoxModuleVersion
	if err := WriteGoxJSON(root, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\ngo 1.23\nrequire "+buildinfo.GoxModule+" "+buildinfo.GoxModuleVersion+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSDK(root, "/nonexistent/go"); err != nil {
		t.Fatal(err)
	}
}
func TestEnsureSDKFailureRestoresFiles(t *testing.T) {
	root := t.TempDir()
	old := []byte("module demo\ngo 1.23\nrequire github.com/gox-lang/gox v1.0.0\n")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), old, 0600); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(root, "go")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf broken > go.mod\nprintf broken > go.sum\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSDK(root, fake); err == nil {
		t.Fatal("expected failure")
	}
	got, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || string(got) != string(old) {
		t.Fatalf("go.mod not restored: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.sum")); !os.IsNotExist(err) {
		t.Fatal("go.sum not rolled back")
	}
}

package serverruntime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestCleanupGoxRouteBuildArtifactsKeepsCurrentPair(t *testing.T) {
	root := t.TempDir()
	buildDir := filepath.Join(root, ".build", "pages", "index")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old.go", "old.go.sourcemap.json", "old.so", "current.go", "current.go.sourcemap.json", "current.so"} {
		if err := os.WriteFile(filepath.Join(buildDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	route := &GoxRouteRuntime{
		BuildDir: buildDir, GeneratedPath: filepath.Join(buildDir, "current.go"), PluginPath: filepath.Join(buildDir, "current.so"),
		CompileResult: &goxcompiler.CompileResult{SourceMapPath: filepath.Join(buildDir, "current.go.sourcemap.json")},
	}
	if err := cleanupGoxRouteBuildArtifacts(root, route); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"current.go", "current.go.sourcemap.json", "current.so"} {
		if _, err := os.Stat(filepath.Join(buildDir, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
	for _, name := range []string{"old.go", "old.go.sourcemap.json", "old.so"} {
		if _, err := os.Stat(filepath.Join(buildDir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected %s removed, err=%v", name, err)
		}
	}
}

func TestCleanupDistributedArtifactsHonorsGraceAndKeepsManifest(t *testing.T) {
	root := t.TempDir()
	buildDir := filepath.Join(root, ".build", "pages", "index")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old.go", "old.so", "old.manifest.json", "current.go", "current.so", "current.manifest.json"} {
		if err := os.WriteFile(filepath.Join(buildDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-20 * time.Minute)
	for _, name := range []string{"old.go", "old.so", "old.manifest.json"} {
		if err := os.Chtimes(filepath.Join(buildDir, name), oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	route := &GoxRouteRuntime{
		BuildDir: buildDir, BuildVersion: "current",
		GeneratedPath: filepath.Join(buildDir, "current.go"), PluginPath: filepath.Join(buildDir, "current.so"),
	}
	if err := cleanupGoxRouteBuildArtifactsWithGrace(root, route, 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"current.go", "current.so", "current.manifest.json"} {
		if _, err := os.Stat(filepath.Join(buildDir, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
	for _, name := range []string{"old.go", "old.so", "old.manifest.json"} {
		if _, err := os.Stat(filepath.Join(buildDir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected %s removed, err=%v", name, err)
		}
	}
}

func TestCleanupGoxRouteBuildArtifactsRejectsOutsidePages(t *testing.T) {
	root := t.TempDir()
	buildDir := filepath.Join(root, "unsafe")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	route := &GoxRouteRuntime{BuildDir: buildDir, GeneratedPath: filepath.Join(buildDir, "current.go"), PluginPath: filepath.Join(buildDir, "current.so")}
	if err := cleanupGoxRouteBuildArtifacts(root, route); err == nil {
		t.Fatal("expected safety error")
	}
}

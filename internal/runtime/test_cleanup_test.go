package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupTestBuildArtifactsRemovesUniqueBuildDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tests", "index-build")
	artifact := filepath.Join(dir, "test.so")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("plugin"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CleanupTestBuildArtifacts(&LoadedTest{ArtifactPath: artifact}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("build directory still exists: %v", err)
	}
}

func TestValidateGeneratedTestFunctionsRejectsInvalidSignature(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.go")
	source := "package generated\ntype Module struct{}\nfunc (m *Module) InvalidTest(value string) {}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	err := validateGeneratedTestFunctions(path)
	if err == nil || !strings.Contains(err.Error(), "InvalidTest must have signature func()") {
		t.Fatalf("error = %v", err)
	}
}

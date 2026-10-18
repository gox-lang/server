package gox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeDoesNotWriteOutput(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "index.gox")
	child := filepath.Join(dir, "child.gox")
	out := filepath.Join(dir, "out.go")

	if err := os.WriteFile(entry, []byte(`module main

import "child.gox"

func GET() string { return "ok" }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`module child

func Value() string { return "child" }
`), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := Analyze(entry)
	if err != nil {
		t.Fatal(err)
	}
	if result.Written {
		t.Fatalf("Analyze marked result as written")
	}
	if result.OutPath != "" || result.OutAbsPath != "" {
		t.Fatalf("Analyze should not set output paths: %#v", result)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("Analyze wrote unexpected output file")
	}
	if result.ModuleCount != 2 {
		t.Fatalf("ModuleCount=%d, want 2", result.ModuleCount)
	}
	if len(result.Modules) != 2 {
		t.Fatalf("len(Modules)=%d, want 2", len(result.Modules))
	}
	for _, mod := range result.Modules {
		if mod.DeclaredModuleName == "" {
			t.Fatalf("module %s has empty DeclaredModuleName", mod.Path)
		}
		if mod.DeclaredModuleName != strings.ToLower(mod.DeclaredModuleName) {
			t.Fatalf("DeclaredModuleName should preserve lowercase declaration, got %q", mod.DeclaredModuleName)
		}
	}
}

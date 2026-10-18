package gox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/goxerrors"
)

func TestValidateProjectGoGoroutineSafetyRejectsUnsafeLocalGo(t *testing.T) {
	root := t.TempDir()
	writeLocalGoSafetyTestFile(t, filepath.Join(root, "worker", "worker.go"), `package worker
func Start() { go work() }
func work() {}
`)

	err := validateProjectGoGoroutineSafety(root)
	var goxErr *goxerrors.GoxError
	if !errors.As(err, &goxErr) {
		t.Fatalf("error = %v, want *goxerrors.GoxError", err)
	}
	if goxErr.Kind != diagnosticUnsafeGoroutine {
		t.Fatalf("Kind = %q, want %q", goxErr.Kind, diagnosticUnsafeGoroutine)
	}
	if goxErr.File != "worker/worker.go" || goxErr.Line != 2 {
		t.Fatalf("location = %s:%d:%d, want worker/worker.go:2", goxErr.File, goxErr.Line, goxErr.Column)
	}
}

func TestValidateProjectGoGoroutineSafetyAcceptsSafeLocalGoAcrossFiles(t *testing.T) {
	root := t.TempDir()
	writeLocalGoSafetyTestFile(t, filepath.Join(root, "worker", "start.go"), `package worker
func Start() { go work() }
`)
	writeLocalGoSafetyTestFile(t, filepath.Join(root, "worker", "work.go"), `package worker
func work() {
    defer func() { recover() }()
    run()
}
func run() {}
`)

	if err := validateProjectGoGoroutineSafety(root); err != nil {
		t.Fatalf("validateProjectGoGoroutineSafety() error = %v", err)
	}
}

func TestValidateProjectGoGoroutineSafetyIgnoresTestsAndExternalTrees(t *testing.T) {
	root := t.TempDir()
	unsafe := `package ignored
func Start() { go func() { panic("boom") }() }
`
	writeLocalGoSafetyTestFile(t, filepath.Join(root, "worker", "worker_test.go"), unsafe)
	writeLocalGoSafetyTestFile(t, filepath.Join(root, ".build", "generated.go"), unsafe)
	writeLocalGoSafetyTestFile(t, filepath.Join(root, ".toolchain", "src", "tool.go"), unsafe)
	writeLocalGoSafetyTestFile(t, filepath.Join(root, "vendor", "example.com", "dep", "dep.go"), unsafe)
	writeLocalGoSafetyTestFile(t, filepath.Join(root, "node_modules", "pkg", "tool.go"), unsafe)

	if err := validateProjectGoGoroutineSafety(root); err != nil {
		t.Fatalf("validateProjectGoGoroutineSafety() error = %v", err)
	}
}

func TestAnalyzeRejectsUnsafeGoAnywhereInProject(t *testing.T) {
	root := t.TempDir()
	writeLocalGoSafetyTestFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	entry := filepath.Join(root, "pages", "index.gox")
	writeLocalGoSafetyTestFile(t, entry, "module index\n\nfunc GET(ctx *gox.Context) gox.Page { return \"ok\" }\n")
	// This package is deliberately not imported by the .gox entry. The rule is
	// project-wide for local Go source, not limited to the compile import graph.
	writeLocalGoSafetyTestFile(t, filepath.Join(root, "background", "worker.go"), `package background
func Start() { go func() { panic("boom") }() }
`)

	_, err := Analyze(entry)
	var goxErr *goxerrors.GoxError
	if !errors.As(err, &goxErr) || goxErr.Kind != diagnosticUnsafeGoroutine {
		t.Fatalf("Analyze() error = %#v, want unsafe-goroutine", err)
	}
	if goxErr.File != "background/worker.go" {
		t.Fatalf("File = %q, want background/worker.go", goxErr.File)
	}
}

func writeLocalGoSafetyTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

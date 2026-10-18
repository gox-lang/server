package lsp

import (
	"go/build"
	"os"
	"path/filepath"
	"testing"
)

// Phase 15.3c-C2-C3c specializes standard-library package-name discovery for a
// valid installed GOROOT: skip inactive files, then stop after the first active
// non-test Go file. The exhaustive C2-C2 oracle remains responsible for proving
// that this optimization preserves the real stdlib inventory.
func TestPhase15_3CC2C3CStandardLibraryPackageNameStopsAfterFirstActiveFile(t *testing.T) {
	directory := t.TempDir()
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("00_inactive.go", "//go:build windows\n\npackage wrongwindows\n")
	write("10_active.go", "package selected\n")
	// This file deliberately has an invalid package clause. The stdlib probe
	// must not parse it after a valid active file has already identified the
	// package directory.
	write("20_later.go", "package\n")
	write("30_test.go", "package wrongtest\n")

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	context := build.Default
	context.GOOS = "linux"
	context.GOARCH = "amd64"

	name, ok := standardLibraryGoPackageName(context, directory, entries)
	if !ok {
		t.Fatal("standard-library package probe did not find the first active Go file")
	}
	if name != "selected" {
		t.Fatalf("standard-library package name = %q, want %q", name, "selected")
	}
}

func TestPhase15_3CC2C3CStandardLibraryPackageNameRejectsDirectoryWithoutActiveFiles(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "only_windows.go"), []byte("//go:build windows\n\npackage windows\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "only_test.go"), []byte("package tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	context := build.Default
	context.GOOS = "linux"
	context.GOARCH = "amd64"

	if name, ok := standardLibraryGoPackageName(context, directory, entries); ok || name != "" {
		t.Fatalf("standard-library package probe = (%q, %v), want no active package", name, ok)
	}
}

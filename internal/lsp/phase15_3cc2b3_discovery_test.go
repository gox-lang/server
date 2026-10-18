package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPhase15_3CC2B3DiscoverUsesWorkspaceScannerWithoutDroppingStdOrDependencies(t *testing.T) {
	root := t.TempDir()
	dependencyRoot := t.TempDir()

	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example.com/workspace\n\ngo 1.24\n\nrequire example.com/library v0.0.0\nreplace example.com/library => "+filepath.ToSlash(dependencyRoot)+"\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "localpkg", "local.go"), "package localpkg\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "onlytest", "only_test.go"), "package onlytest\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "command", "main.go"), "package main\n")

	writeGoPackageHoverFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/library\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(dependencyRoot, "library.go"), "package toolkit\n")

	candidates, err := NewGoPackageIndex().Discover(root)
	if err != nil {
		t.Fatal(err)
	}

	requireGoPackageCandidate(t, candidates, "strings", "strings")
	requireGoPackageCandidate(t, candidates, "example.com/workspace/localpkg", "localpkg")
	requireGoPackageCandidate(t, candidates, "example.com/library", "toolkit")

	forbidGoPackageCandidate(t, candidates, "example.com/workspace/onlytest")
	forbidGoPackageCandidate(t, candidates, "example.com/workspace/command")
}

func TestPhase15_3CC2B3DiscoverRebuildAfterInvalidationStillUsesWorkspaceScanner(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example.com/workspace\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "first", "first.go"), "package first\n")

	index := NewGoPackageIndex()
	candidates, err := index.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	requireGoPackageCandidate(t, candidates, "example.com/workspace/first", "first")

	writeGoPackageHoverFile(t, filepath.Join(root, "second", "second.go"), "package second\n")
	if err := os.Remove(filepath.Join(root, "first", "first.go")); err != nil {
		t.Fatal(err)
	}
	index.InvalidateRoot(root)

	candidates, err = index.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	forbidGoPackageCandidate(t, candidates, "example.com/workspace/first")
	requireGoPackageCandidate(t, candidates, "example.com/workspace/second", "second")
}

func requireGoPackageCandidate(t *testing.T, candidates []GoPackageCandidate, path, name string) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.Path == path && candidate.Name == name {
			return
		}
	}
	t.Fatalf("missing package candidate %q (%q): %#v", path, name, candidates)
}

func forbidGoPackageCandidate(t *testing.T, candidates []GoPackageCandidate, path string) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.Path == path {
			t.Fatalf("unexpected package candidate %q: %#v", path, candidate)
		}
	}
}
